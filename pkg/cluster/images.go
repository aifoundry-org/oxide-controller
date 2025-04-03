package cluster

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"

	"github.com/aifoundry-org/oxide-controller/pkg/util"
	"github.com/oxidecomputer/oxide.go/oxide"
)

// ensureImagesExist checks if the right images exist and creates them if needed
// they can exist at the silo or project level. However, if they do not exist, then they
// will be created at the project level.
func ensureImagesExist(ctx context.Context, client *oxide.Client, projectID string, images ...Image) ([]string, error) {
	// TODO: We don't need to list images, we can `View` them by name -
	//       `images` array is never long, few more requests shouldn't harm.
	// TODO: Do we need pagination? Using arbitrary limit for now.
	existing, err := client.ImageList(ctx, oxide.ImageListParams{Project: oxide.NameOrId(projectID), Limit: oxide.NewPointer(32)})
	if err != nil {
		return nil, fmt.Errorf("failed to list images: %w", err)
	}
	var (
		missingImages []Image
		imageMap      = make(map[string]string)
		idMap         = make(map[string]string)
	)
	for _, image := range existing.Items {
		imageMap[string(image.Name)] = image.Id
	}
	for _, image := range images {
		if _, ok := imageMap[image.Name]; !ok {
			missingImages = append(missingImages, image)
		} else {
			idMap[image.Name] = imageMap[image.Name]
		}
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
		// FIXME?: round to the nearest GB: not verified - the default debian image is *exactly* 3GB :)
		//   https://github.com/oxidecomputer/oxide.rs/blob/17e3f58248832f977d366afdc69641551a62b1db/sdk/src/extras/disk.rs#L735
		// round up to nearest block size
		size = (size + blockSize) &^ blockSize
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
		imageMap[missingImage.Name] = image.Id
		idMap[missingImage.Name] = image.Id
	}

	// go through all of the image names and get their IDs
	var ids []string
	for _, image := range images {
		if id, ok := imageMap[image.Name]; !ok {
			return nil, fmt.Errorf("image '%s' does not exist", image.Name)
		} else {
			ids = append(ids, id)
		}
	}
	return ids, nil
}
