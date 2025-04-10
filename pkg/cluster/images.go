package cluster

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"

	"github.com/aifoundry-org/oxide-controller/pkg/util"
	"github.com/oxidecomputer/oxide.go/oxide"
	log "github.com/sirupsen/logrus"
)

// ensureImagesExist checks if the right images exist and creates them if needed
// they can exist at the silo or project level. However, if they do not exist, then they
// will be created at the project level.
// The returned images will have their IDs set. It will have the exact same number of images as in the argument images.
// This is not a member function of Cluster, as it can be self-contained and therefore tested.
func ensureImagesExist(ctx context.Context, logger *log.Entry, client *oxide.Client, projectID string, images ...Image) ([]Image, error) {
	// TODO: We don't need to list images, we can `View` them by name -
	//       `images` array is never long, few more requests shouldn't harm.
	// TODO: Do we need pagination? Using arbitrary limit for now.
	logger.Debugf("Listing images for project %s", projectID)
	projectImages, err := client.ImageListAllPages(ctx, oxide.ImageListParams{Project: oxide.NameOrId(projectID), Limit: oxide.NewPointer(32)})
	if err != nil {
		return nil, fmt.Errorf("failed to list project images: %w", err)
	}
	logger.Debugf("total project images %d", len(projectImages))
	logger.Debugf("Listing global images for sled")
	globalImages, err := client.ImageListAllPages(ctx, oxide.ImageListParams{Limit: oxide.NewPointer(32)})
	if err != nil {
		return nil, fmt.Errorf("failed to list global images: %w", err)
	}
	logger.Debugf("total global images %d", len(globalImages))
	var (
		missingImages   []Image
		projectImageMap = make(map[string]*oxide.Image)
		globalImageMap  = make(map[string]*oxide.Image)
		imageMap        = make(map[string]*oxide.Image)
		idMap           = make(map[string]*oxide.Image)
	)
	for _, image := range projectImages {
		projectImageMap[string(image.Name)] = &image
	}
	for _, image := range globalImages {
		globalImageMap[string(image.Name)] = &image
	}
	for i := range images {
		if image, ok := projectImageMap[images[i].Name]; ok {
			logger.Infof("Image %s found in project images", images[i].Name)
			name := images[i].Name
			idMap[name] = image
			images[i].ID = image.Id
			images[i].Size = int(image.Size)
			imageMap[name] = image
			continue
		}
		if image, ok := globalImageMap[images[i].Name]; ok {
			logger.Infof("Image %s found in global images", images[i].Name)
			name := images[i].Name
			idMap[name] = image
			images[i].ID = image.Id
			images[i].Size = int(image.Size)
			imageMap[name] = image
			continue
		}
		// did not find it in either
		logger.Infof("Image %+v not found, adding to list", images[i])
		missingImages = append(missingImages, images[i])
	}

	for _, missingImage := range missingImages {
		snapshotName := fmt.Sprintf("snapshot-%s", missingImage.Name)
		// how to create the image? oxide makes this a bit of a pain, you need to do multiple steps:
		// 1. Download the image from the URL locally to a temporary file
		// 2. Determine the size of the image
		// 3. Create a blank disk of that size or larger, rounded up to nearest block size at least
		//      https://docs.oxide.computer/api/disk_create
		// 4. Import base64 blobs of data from the disk to the blank disk
		//      https://docs.oxide.computer/api/disk_bulk_write_import_start
		//      https://docs.oxide.computer/api/disk_bulk_write_import
		// 	    https://docs.oxide.computer/api/disk_bulk_write_import_stop
		// 5. Finalize the import by making a snapshot of the disk
		//      https://docs.oxide.computer/api/disk_finalize_import
		// 6. Create an image from the snapshot
		//      https://docs.oxide.computer/api/image_create
		file, err := os.CreateTemp("", "image-")
		if err != nil {
			return nil, fmt.Errorf("failed to create temporary file: %w", err)
		}
		defer os.RemoveAll(file.Name())
		if err := util.DownloadFile(file.Name(), missingImage.Source); err != nil {
			return nil, fmt.Errorf("failed to download image: %w", err)
		}
		stat, err := file.Stat()
		if err != nil {
			return nil, fmt.Errorf("failed to get file size: %w", err)
		}
		size := stat.Size()
		if size == 0 {
			return nil, fmt.Errorf("image file is empty")
		}
		size = util.RoundUp(size, GB)
		// create the disk
		disk, err := client.DiskCreate(ctx, oxide.DiskCreateParams{
			Project: oxide.NameOrId(projectID),
			Body: &oxide.DiskCreate{
				Description: fmt.Sprintf("Disk for image '%s'", missingImage.Name),
				Size:        oxide.ByteCount(size),
				Name:        oxide.Name(missingImage.Name),
				DiskSource: oxide.DiskSource{
					Type:      oxide.DiskSourceTypeImportingBlocks,
					BlockSize: blockSize, // TODO: Must be multiple of image size. Verify?
				},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create disk: %w", err)
		}
		// import the data
		if err := client.DiskBulkWriteImportStart(ctx, oxide.DiskBulkWriteImportStartParams{
			Disk: oxide.NameOrId(disk.Id),
		}); err != nil {
			return nil, fmt.Errorf("failed to start bulk write import: %w", err)
		}
		// write in 0.5MB chunks or until finished
		f, err := os.Open(file.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to open file: %w", err)
		}
		defer f.Close()
		var offset int
		for {
			buf := make([]byte, MB/2)
			n, err := f.Read(buf)
			if err != nil && err != io.EOF {
				return nil, fmt.Errorf("failed to read file: %w", err)
			}
			if n == 0 {
				break
			}
			// convert the read data into base64. Why? because that is what oxide wants
			if err := client.DiskBulkWriteImport(ctx, oxide.DiskBulkWriteImportParams{
				Disk: oxide.NameOrId(disk.Id),
				Body: &oxide.ImportBlocksBulkWrite{
					Base64EncodedData: base64.StdEncoding.EncodeToString(buf[:n]),
					Offset:            &offset,
				},
			}); err != nil {
				return nil, fmt.Errorf("failed to write data: %w", err)
			}
			offset += n
		}
		if err := client.DiskBulkWriteImportStop(ctx, oxide.DiskBulkWriteImportStopParams{
			Disk: oxide.NameOrId(disk.Id),
		}); err != nil {
			return nil, fmt.Errorf("failed to stop bulk write import: %w", err)
		}
		// finalize the import
		if err := client.DiskFinalizeImport(ctx, oxide.DiskFinalizeImportParams{
			Disk: oxide.NameOrId(disk.Id),
			Body: &oxide.FinalizeDisk{
				SnapshotName: oxide.Name(snapshotName),
			},
		}); err != nil {
			return nil, fmt.Errorf("failed to finalize import: %w", err)
		}

		client.DiskDelete(ctx, oxide.DiskDeleteParams{
			Disk: oxide.NameOrId(disk.Id),
		})

		// Find snapshot Id by name.
		snapshot, err := client.SnapshotView(ctx, oxide.SnapshotViewParams{
			Snapshot: oxide.NameOrId(snapshotName), Project: oxide.NameOrId(projectID),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to find snapshot: %w", err)
		}

		image, err := client.ImageCreate(ctx, oxide.ImageCreateParams{
			Project: oxide.NameOrId(projectID),
			Body: &oxide.ImageCreate{
				Name:        oxide.Name(missingImage.Name),
				Description: fmt.Sprintf("Image for '%s'", missingImage.Name),
				Source: oxide.ImageSource{
					Type: oxide.ImageSourceTypeSnapshot,
					Id:   snapshot.Id,
				},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create image: %w", err)
		}
		imageMap[missingImage.Name] = image
		idMap[missingImage.Name] = image
	}

	// go through all of the image names and save their IDs
	for i := range images {
		if oxImage, ok := imageMap[images[i].Name]; !ok {
			return nil, fmt.Errorf("image '%s' does not exist", images[i].Name)
		} else {
			images[i].ID = oxImage.Id
			images[i].Size = int(oxImage.Size)
		}
	}
	return images, nil
}
