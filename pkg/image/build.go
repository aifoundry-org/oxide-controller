package image

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/stream"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

func Bundle(refStr, inpath, outpath string) error {
	ref, err := name.ParseReference(refStr)
	if err != nil {
		return fmt.Errorf("parsing reference: %w", err)
	}

	// Create the layer with the binary at /controller
	layer, err := binaryLayer(inpath, "/controller")
	if err != nil {
		return fmt.Errorf("creating layer: %w", err)
	}

	// Start with empty image and append the layer
	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		return fmt.Errorf("appending layer: %w", err)
	}

	// Set entrypoint and permissions
	cfg := &v1.ConfigFile{
		Config: v1.Config{
			Entrypoint: []string{"/controller"},
		},
	}
	img, err = mutate.ConfigFile(img, cfg)
	if err != nil {
		return fmt.Errorf("configuring image: %w", err)
	}

	// Write to OCI tarball
	f, err := os.Create(outpath)
	if err != nil {
		return fmt.Errorf("creating output tarball: %w", err)
	}
	defer f.Close()

	if err := tarball.Write(ref, img, f); err != nil {
		return fmt.Errorf("writing tarball: %w", err)
	}

	return nil
}

func binaryLayer(binPath, targetPath string) (v1.Layer, error) {
	pr, pw := io.Pipe()

	go func() {
		tw := tar.NewWriter(pw)

		// Open binary file for streaming
		f, err := os.Open(binPath)
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		defer f.Close()

		info, err := f.Stat()
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}

		// Write tar header
		hdr := &tar.Header{
			Name:    filepath.Base(targetPath), // no leading slash
			Mode:    0755,
			Size:    info.Size(),
			ModTime: time.Now(),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			_ = pw.CloseWithError(err)
			return
		}

		// Stream the file into the tar writer
		if _, err := io.Copy(tw, f); err != nil {
			_ = pw.CloseWithError(err)
			return
		}

		// Finalize tar and close
		if err := tw.Close(); err != nil {
			_ = pw.CloseWithError(err)
			return
		}

		_ = pw.Close()
	}()

	// Return a streamed layer that consumes the pipe reader
	return stream.NewLayer(pr), nil
}
