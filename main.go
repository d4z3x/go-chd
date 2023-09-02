package main

import (
	"archive/zip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"log/slog"
)

func replaceExt(path string, newExt string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	ext := filepath.Ext(path)
	var newName string
	if ext != "" {
		newName = filepath.Join(dir, strings.Replace(base, ext, newExt, 1))
	}
	return newName
}

func decompressZIP(zipFile string) bool {
	dst := replaceExt(zipFile, "")
	archive, err := zip.OpenReader(zipFile)
	if err != nil {
		log.Error("Can't open ZIP file", "file", zipFile, "error", err)
	}
	defer archive.Close()

	// log.Info("Unzipping", "file", zipFile)
	filesInZip := []string{}
	for _, f := range archive.File {
		filesInZip = append(filesInZip, f.Name)
	}

	containsCUEISO := slices.ContainsFunc(filesInZip, func(s string) bool {
		return strings.HasSuffix(s, ".cue") || strings.HasSuffix(s, ".iso")
	})

	if !containsCUEISO {
		log.Info("No CUE or ISO files found", "file", zipFile)
		return false
	}

	log.Info("Unzipping ZIP", "count", len(filesInZip), "file", zipFile)
	for _, f := range archive.File {
		filePath := filepath.Join(dst, f.Name)

		if !strings.HasPrefix(filePath, filepath.Clean(dst)+string(os.PathSeparator)) {
			log.Error("invalid file path", "dir", filePath)
			return false
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(filePath, os.ModePerm); err != nil {
				log.Error("Can't make dir for ZIP", "dir", filePath)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(filePath), os.ModePerm); err != nil {
			log.Error("Can't make dir for ZIP", "dir", filePath)
			panic(err)
		}

		dstFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			panic(err)
		}

		fileInArchive, err := f.Open()
		if err != nil {
			panic(err)
		}

		if _, err := io.Copy(dstFile, fileInArchive); err != nil {
			panic(err)
		}

		dstFile.Close()
		fileInArchive.Close()
	}
	return true
}

var log = slog.New(slog.NewTextHandler(os.Stdout, nil))

func main() {
	entries, err := os.ReadDir("./")
	if err != nil {
		log.Info("Couldn't read dir", "error", err)
		return
	}

	var (
		zips           []string
		convertedToCHD int
	)

	for _, e := range entries {
		if !e.IsDir() && !strings.HasPrefix(e.Name(), "._") && strings.HasSuffix(e.Name(), ".zip") {
			zips = append(zips, e.Name())
		}
	}

	log.Info("** gochd **", "ZIP_count", len(zips), "numprocs", runtime.NumCPU())
	curDir, errCurDir := os.Getwd()
	if errCurDir != nil {
		log.Error("Can't get current dir", "error", errCurDir)
		return
	}

	for _, zip := range zips {
		zipHasCUEorISO := decompressZIP(zip)
		if !zipHasCUEorISO {
			continue
		}
		dir := replaceExt(zip, "")
		if err := os.Chdir(dir); err != nil {
			continue
		}

		var files []string
		files, _ = filepath.Glob("*.cue")
		if len(files) == 0 {
			files, _ = filepath.Glob("*.iso")
			if len(files) == 0 {
				log.Error("No CUE or ISO found, removing unzipped dir")
				if errDir := os.RemoveAll(dir); errDir != nil {
					log.Error("Can't remove removing dir", "dir", dir, "error", errDir)
				}
				return
			}
		}

		commands := []string{
			"chdman",
			"createcd",
			"-np", strconv.Itoa(runtime.NumCPU() - 1),
			"-f", "-i", files[0],
			"-o", filepath.Join("..", replaceExt(files[0], ".chd")),
		}

		cmd := exec.Command(commands[0], commands[1:]...)

		if errCmd := cmd.Run(); errCmd != nil {
			log.Error("Failed to process via CHD", "file", files[0], "error", errCmd)
			return
		}

		// os.Rename(replaceExt(files[0], ".chd"), "../"+replaceExt(files[0], ".chd"))
		if err := os.Chdir(curDir); err != nil {
			log.Error("Can't change back to current dir", "dir", curDir)
			return
		}
		if err := os.Remove(replaceExt(files[0], ".zip")); err != nil {
			log.Error("Can't remove ZIP", "file", replaceExt(files[0], ".zip"))
			return
		}

		if errDir := os.RemoveAll(dir); errDir != nil {
			log.Error("Can't remove expanded dir", "error", errDir)
			return
		}
		log.Info("SUCCESS - Created CHD", "file", replaceExt(files[0], ".chd"))
		convertedToCHD++
	}
	log.Info("Converted CHD files", "count", convertedToCHD, "CHD_Count", convertedToCHD, "ZIP_Count", len(zips))
}
