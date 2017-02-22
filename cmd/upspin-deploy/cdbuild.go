// Copyright 2016 Google Inc. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

// Copied from github.com/broady/cdbuild.
// TODO(adg): clean this up.

package main // import "upspin.io/cmd/upspin-deploy"

import (
	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	cstorage "cloud.google.com/go/storage"
	"golang.org/x/net/context"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/cloudbuild/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/storage/v1"
)

func cdbuild(dir, projectID, name, pkgPath string) error {
	stagingBucket := projectID + "-cdbuild"
	buildObject := fmt.Sprintf("build/%s-%s.tar.gz", name, randomID())

	ctx := context.Background()
	hc, err := google.DefaultClient(ctx, storage.CloudPlatformScope)
	if err != nil {
		return fmt.Errorf("Could not get authenticated HTTP client: %v", err)
	}

	log.Printf("Pushing code to gs://%s/%s", stagingBucket, buildObject)

	if err := uploadTar(ctx, dir, hc, stagingBucket, buildObject); err != nil {
		return fmt.Errorf("Could not upload source: %v", err)
	}

	api, err := cloudbuild.New(hc)
	if err != nil {
		return fmt.Errorf("Could not get cloudbuild client: %v", err)
	}

	var steps []*cloudbuild.BuildStep
	if pkgPath != "" {
		steps = append(steps, &cloudbuild.BuildStep{
			Name: "gcr.io/" + projectID + "/cloudbuild",
			Args: []string{"install", pkgPath},
		})
	}
	steps = append(steps, &cloudbuild.BuildStep{
		Name: "gcr.io/cloud-builders/docker",
		Args: []string{"build", "--tag=gcr.io/" + projectID + "/" + name, "."},
	})
	call := api.Projects.Builds.Create(projectID, &cloudbuild.Build{
		LogsBucket: stagingBucket,
		Source: &cloudbuild.Source{
			StorageSource: &cloudbuild.StorageSource{
				Bucket: stagingBucket,
				Object: buildObject,
			},
		},
		Steps:  steps,
		Images: []string{"gcr.io/" + projectID + "/" + name},
	})
	op, err := call.Context(ctx).Do()
	if err != nil {
		if gerr, ok := err.(*googleapi.Error); ok {
			if gerr.Code == 404 {
				// HACK(cbro): the API does not return a good error if the API is not enabled.
				fmt.Fprintln(os.Stderr, "Could not create build. It's likely the Cloud Container Builder API is not enabled.")
				fmt.Fprintf(os.Stderr, "Go here to enable it: https://console.cloud.google.com/apis/api/cloudbuild.googleapis.com/overview?project=%s\n", projectID)
				os.Exit(1)
			}
		}
		return fmt.Errorf("Could not create build: %#v", err)
	}
	remoteID, err := getBuildID(op)
	if err != nil {
		return fmt.Errorf("Could not get build ID from op: %v", err)
	}

	log.Printf("Logs at https://console.cloud.google.com/m/cloudstorage/b/%s/o/log-%s.txt", stagingBucket, remoteID)

	fail := false
	for {
		b, err := api.Projects.Builds.Get(projectID, remoteID).Do()
		if err != nil {
			return fmt.Errorf("Could not get build status: %v", err)
		}

		if s := b.Status; s != "WORKING" && s != "QUEUED" {
			if b.Status == "FAILURE" {
				fail = true
			}
			log.Printf("Build status: %v", s)
			break
		}

		time.Sleep(time.Second)
	}

	c, err := cstorage.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("Could not make Cloud storage client: %v", err)
	}
	defer c.Close()
	if err := c.Bucket(stagingBucket).Object(buildObject).Delete(ctx); err != nil {
		return fmt.Errorf("Could not delete source tar.gz: %v", err)
	}
	log.Print("Cleaned up.")
	if fail {
		return fmt.Errorf("cdbuild failed")
	}

	return nil
}

// HACK: workaround for lack of type for "Metadata" field.
func getBuildID(op *cloudbuild.Operation) (string, error) {
	if op.Metadata == nil {
		return "", errors.New("missing Metadata in operation")
	}
	var buildMeta cloudbuild.BuildOperationMetadata
	if err := json.Unmarshal([]byte(op.Metadata), &buildMeta); err != nil {
		return "", err
	}
	if buildMeta.Build == nil {
		return "", errors.New("missing Build in operation metadata")
	}
	return buildMeta.Build.Id, nil
}

func uploadTar(ctx context.Context, root string, hc *http.Client, bucket string, objectName string) error {
	c, err := cstorage.NewClient(ctx)
	if err != nil {
		return err
	}
	defer c.Close()

	w := c.Bucket(bucket).Object(objectName).NewWriter(ctx)
	gzw := gzip.NewWriter(w)
	tw := tar.NewWriter(gzw)

	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if path == root {
			return nil
		}
		relpath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info = renamingFileInfo{info, relpath}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	}); err != nil {
		w.CloseWithError(err)
		return err
	}
	if err := tw.Close(); err != nil {
		w.CloseWithError(err)
		return err
	}
	if err := gzw.Close(); err != nil {
		w.CloseWithError(err)
		return err
	}
	return w.Close()
}

type renamingFileInfo struct {
	os.FileInfo
	name string
}

func (fi renamingFileInfo) Name() string {
	return fi.name
}

func randomID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", b)
}
