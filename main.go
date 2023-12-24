package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/bodgit/sevenzip"

	"log/slog"

	"github.com/creack/pty"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/term"
)

//https://go.dev/play/p/nE3HLTvMu3v

func replaceExt(path string, newExt string) (newName string) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	ext := filepath.Ext(path)
	if ext != "" {
		newName = filepath.Join(dir, strings.Replace(base, ext, newExt, 1))
	}
	return
}

func decompressZIP(zipFile string) bool {

	dst := replaceExt(zipFile, "")
	archive, err := zip.OpenReader(zipFile)
	if err != nil {
		log.Error("Can't open ZIP file", "file", zipFile, "error", err)
		if err == zip.ErrFormat {
			slog.Error("Renamed bad ZIP file", "file", zipFile+".bad")
			os.Rename(zipFile, zipFile+".bad")
			return false
		}

	}
	defer archive.Close()

	filesInZip := []string{}
	dirsInZip := []bool{}

	singleEmbeddedDirOrNone := false
	var dirNameIfEmbedded string

	for _, f := range archive.File {
		filesInZip = append(filesInZip, f.Name)
		log.Info("dirs", "dirname", f.Name, "bool", f.FileHeader.FileInfo().IsDir())
		if f.FileHeader.FileInfo().IsDir() {
			dirNameIfEmbedded = f.Name
			dirsInZip = append(dirsInZip, f.FileHeader.FileInfo().IsDir())
			log.Info("IsDir", "dir", dirNameIfEmbedded)
		}
	}
	log.Info("dirsInZip", "dircount", len(dirsInZip), "dirsInZip", dirsInZip)
	singleEmbeddedDirOrNone = len(dirsInZip) == 1 || len(dirsInZip) == 0

	log.Info("singleEmbeddedDirOrNone", "bool", singleEmbeddedDirOrNone, "dirname", dirNameIfEmbedded)

	checkForCUEISO := checkIfCueOrIso(filesInZip)
	if !checkForCUEISO {
		log.Info("No CUE or ISO files found", "file", zipFile)
		return false
	}

	log.Info("checkForCUEISO", "check", checkForCUEISO)
	log.Info("Unzipping ZIP", "count", len(filesInZip), "file", zipFile)
	bar := progressbar.Default(int64(len(filesInZip)))
	var filePath string

	if err := os.MkdirAll(dst, os.ModePerm); err != nil {
		log.Error("Can't make directory as ZIP name", "dir", dst)
		panic(err)
	}

	for _, f := range archive.File {
		bar.Add(1)
		if !f.FileInfo().IsDir() {
			base := filepath.Base(f.Name)
			filePath = filepath.Join(dst, base)
			log.Info("Just file", "file", filePath)
		}

		// if !strings.HasPrefix(filePath, filepath.Clean(dst)+string(os.PathSeparator)) {
		// 	log.Error("invalid file path", "dir", filePath)
		// 	return false
		// }

		if f.FileInfo().IsDir() {
			log.Info("Not creating dir from arch", "dirname", f.Name)
			// if err := os.MkdirAll(filePath, os.ModePerm); err != nil {
			// 	log.Error("Can't make dir for ZIP", "dir", filePath)
			// }
			continue
		}

		log.Info("** filepath.Dir", "value", filepath.Dir(filePath))

		// if err := os.MkdirAll(filepath.Dir(filePath), os.ModePerm); err != nil {
		// 	log.Error("Can't make dir for ZIP", "dir", filePath)
		// 	panic(err)
		// }

		dstFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			log.Info("Open file", "name", dstFile, "file", filePath)
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
	log.Info("returning from func")
	return true
}

func checkIfCueOrIso(filesInZip []string) bool {
	hasCUE := slices.ContainsFunc(filesInZip, func(s string) bool {
		return strings.HasSuffix(strings.ToLower(s), ".cue")
	})

	hasISO := slices.ContainsFunc(filesInZip, func(s string) bool {
		return strings.HasSuffix(strings.ToLower(s), ".iso")
	})

	return (hasCUE || hasISO)
}

func decompress7z(zipFile string) bool {

	dst := replaceExt(zipFile, "")
	archive, err := sevenzip.OpenReader(zipFile)
	if err != nil {
		log.Error("Can't open 7z file", "file", zipFile, "error", err)
		slog.Error("Renamed bad 7z file", "file", zipFile+".bad")
		os.Rename(zipFile, zipFile+".bad")
		return false
	}
	defer archive.Close()

	// log.Info("Unzipping", "file", zipFile)
	filesInZip := []string{}
	for _, f := range archive.File {
		filesInZip = append(filesInZip, f.Name)
	}

	checkForCUEISO := checkIfCueOrIso(filesInZip)
	if !checkForCUEISO {
		log.Info("No CUE or ISO files found", "file", zipFile)
		return false
	}
	log.Info("checkForCUEISO", "check", checkForCUEISO)

	log.Info("Unzipping 7z", "count", len(filesInZip), "file", zipFile)

	// if err := os.MkdirAll(filepath.Dir(filePath), os.ModePerm); err != nil {
	// 	log.Error("Can't make dir for ZIP", "dir", filePath)
	// 	panic(err)
	// }

	bar := progressbar.Default(int64(len(filesInZip)))
	var wg sync.WaitGroup
	for _, f := range archive.File {

		wg.Add(1)
		go func(f *sevenzip.File) bool {
			defer wg.Done()

			filePath := filepath.Join(dst, f.Name)

			if !strings.HasPrefix(filePath, filepath.Clean(dst)+string(os.PathSeparator)) {
				log.Error("invalid file path", "dir", filePath)
				return false
			}

			if f.FileInfo().IsDir() {
				// if err := os.MkdirAll(filePath, os.ModePerm); err != nil {
				// 	log.Error("Can't make dir for ZIP", "dir", filePath)
				// }
				log.Info("Not making embedded directory", "name", f.FileInfo().IsDir())
				// continue
			}

			// go func() {
			defer bar.Add(1)

			if err := os.MkdirAll(filepath.Dir(filePath), os.ModePerm); err != nil {
				log.Error("Can't make dir for 7z", "dir", filePath)
				panic(err)
			}
			log.Info("** filepath.Dir", "value", filePath)
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
			return true
		}(f)
	}
	wg.Wait()

	return true
}

var log = slog.New(slog.NewTextHandler(os.Stdout, nil))
var chdmanRE = regexp.MustCompile(`Compressing, (\d+\.\d)\% .* \(ratio=(\d+\.\d)\%\)`)
var cueFileRE = regexp.MustCompile(`FILE \"([^/]+)\" BINARY`)

func getRatio(text string) (float32, float32) {
	//Compressing, 86.9% complete... (ratio=25.8%)

	// step[\s]+(\d+)`)
	result := chdmanRE.FindStringSubmatch(text)
	// fmt.Println(result[1], result[2])
	complete, _ := strconv.ParseFloat(result[1], 32)
	ratio, _ := strconv.ParseFloat(result[2], 32)
	return float32(complete), float32(ratio)
}

func runCmdX() error {
	// Create arbitrary command.
	commands := []string{
		"chdman",
		"createcd",
		"-np", strconv.Itoa(runtime.NumCPU() - 1),
		"-f", "-i", "NHL All-Star Hockey 98 (USA)/NHL All-Star Hockey 98 (USA).cue",
		"-o", filepath.Join("..", "t.chd"),
	}
	//Compressing, 86.9% complete... (ratio=25.8%)
	// cmdCtx, cmdDone := context.WithCancel(context.Background())

	c := exec.Command(commands[0], commands[1:]...)

	// c := exec.Command("bash")

	// Start the command with a pty.
	ptmx, err := pty.Start(c)
	if err != nil {
		return err
	}
	// Make sure to close the pty at the end.
	defer func() { _ = ptmx.Close() }() // Best effort.

	// Handle pty size.
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	go func() {
		for range ch {
			if err := pty.InheritSize(os.Stdin, ptmx); err != nil {
				fmt.Printf("error resizing pty: %s", err)
			}
		}
	}()
	ch <- syscall.SIGWINCH                        // Initial resize.
	defer func() { signal.Stop(ch); close(ch) }() // Cleanup signals when done.

	// Set stdin in raw mode.
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		panic(err)
	}
	defer func() { _ = term.Restore(int(os.Stdin.Fd()), oldState) }() // Best effort.

	// Copy stdin to the pty and the pty to stdout.
	// NOTE: The goroutine will keep reading until the next keystroke before returning.
	go func() {
		_, _ = io.Copy(ptmx, os.Stdin)
	}()
	_, _ = io.Copy(os.Stdout, ptmx)

	return nil
}

func main() {
	if err := runCmd(); err != nil {
		fmt.Println(err)
	}
}

func fixCueFileCase(file string) string {
	srcFormat := `FILE "([^/]+)" .*`
	// srcFormat := `(.*)`
	// srcFormat := `FILE "(.*)" BINARY`
	b, err := os.ReadFile(file)
	// can file be opened?
	if err != nil {
		fmt.Print(err)
	}
	// bstr := string(b)
	re := regexp.MustCompile(srcFormat)
	log.Info("cuedump", "contents", string(b))
	// cueFile := re.FindStringSubmatch()
	cueFilename := re.FindStringSubmatch(string(b))
	replacedCue := strings.Replace(string(b), cueFilename[1], strings.ToLower(cueFilename[1]), -1)

	// replacedCue := re.ReplaceAllString(bstr, "$1")
	// newStr := regexp.ReplaceAllString(bstr, "(\\w+) (.*)", "replaced $2")
	fmt.Println("***", cueFilename[1])
	log.Info("cuefile", "file", cueFilename[1], "cue", replacedCue)
	// os.Rename(cueFilename, strings.ToUpper(cueFilename))
	os.WriteFile(file+"2", []byte(replacedCue), 0644)
	log.Info("remove", "file", file)
	os.Remove(file)
	os.Rename(file+"2", file)
	return replacedCue
}

func runCmd() (e error) {
	// getRatio
	e = nil
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
		if !e.IsDir() && !strings.HasPrefix(e.Name(), "._") && (strings.HasSuffix(e.Name(), ".7z") || strings.HasSuffix(e.Name(), ".zip")) {
			zips = append(zips, e.Name())
		}
	}

	log.Info("** gochd **", "ZIP_count", len(zips), "numprocs", runtime.NumCPU())
	curDir, errCurDir := os.Getwd()
	if errCurDir != nil {
		log.Error("Can't get current dir", "error", errCurDir)
		return
	}

	zipHasCUEorISO := false
	// sourceFormatExt := ""
	for _, zip := range zips {
		if strings.HasSuffix(zip, ".zip") {
			// sourceFormatExt = ".zip"
			zipHasCUEorISO = decompressZIP(zip)
		}
		if strings.HasSuffix(zip, ".7z") {
			// sourceFormatExt = ".7z"
			zipHasCUEorISO = decompress7z(zip)
		}

		if !zipHasCUEorISO {
			continue
		}
		dir := replaceExt(zip, "")
		if err := os.Chdir(dir); err != nil {
			continue
		}

		var files []string
		cueFiles, _ := filepath.Glob("*.cue")
		allFiles, _ := filepath.Glob("*")
		isoFiles, _ := filepath.Glob("*.iso")

		log.Info("all", "files", allFiles)
		log.Info("files", "CUE", cueFiles)

		currDir, _ := os.Getwd()
		log.Info("cwd", "currDir", currDir)
		files2, _ := os.ReadDir(currDir)

		log.Info("ReadDir", "files2", files2)
		log.Info("filesGlobCUE", "files", cueFiles)
		if len(cueFiles) == 0 {
			log.Info("filesGlobISO", "files", isoFiles)

			if len(isoFiles) == 0 {
				log.Error("No CUE or ISO found, removing unzipped dir")
				if errDir := os.RemoveAll(dir); errDir != nil {
					log.Error("Can't remove removing dir", "dir", dir, "error", errDir)
				}
				return
			}
		}
		var processFilename string
		if len(cueFiles) > 0 {
			log.Info("isoFiles > 0", "isofiles[0]", cueFiles[0])

			processFilename = cueFiles[0]
			//var cueFilename string
			//fixCueFileCase(cueFilename)

		} else if len(isoFiles) > 0 {
			log.Info("isoFiles > 0", "isofiles[0]", isoFiles[0])
			processFilename = isoFiles[0]
		}

		commands := []string{
			"chdman",
			"createcd",
			"-np", strconv.Itoa(runtime.NumCPU() - 1),
			"-f", "-i", processFilename,
			"-o", filepath.Join("..", dir+".chd"),
		}
		// currDir2, _ := os.Getwd()

		log.Info("exec", "cmd", commands, "dir", dir)

		//Compressing, 86.9% complete... (ratio=25.8%)
		// cmdCtx, cmdDone := context.WithCancel(context.Background())
		cmd := exec.Command(commands[0], commands[1:]...)
		// ptmx, _ := pty.Start(cmd)
		// Make sure to close the pty at the end.
		// defer func() { _ = ptmx.Close() }() // Best effort.

		// stdout, _ := cmd.StdoutPipe()

		// lines := make(chan string)

		var stdBuffer bytes.Buffer
		mw := io.MultiWriter(os.Stdout, &stdBuffer)

		cmd.Stdout = mw
		cmd.Stderr = mw

		errCmd := cmd.Start()

		if errCmd != nil {
			log.Error("Failed to process via CHD", "file", files[0], "error", errCmd)
			return
		}

		cmdWaitErr := cmd.Wait()
		// log.Info("stdout/err", "contents", stdBuffer.String())

		files2, _ = os.ReadDir(".")
		log.Info("ReadDir", "files2", files2)
		files2, _ = os.ReadDir("../chill.chd")
		log.Info("ReadDir", "CHD", files2)

		// cmd.Wait()

		// ctx, _ := context.WithCancel(context.Background())

		// select {
		// case outputx := <-lines:
		// 	// I will do somethign with this!
		// }

		// cmd.Wait()

		// os.Rename(replaceExt(files[0], ".chd"), "../"+replaceExt(files[0], ".chd"))
		if err := os.Chdir(curDir); err != nil {
			log.Error("Can't change back to current dir", "dir", curDir)
			return
		}

		if cmdWaitErr == nil {
			if err := os.Remove(zip); err != nil {
				log.Error("Can't remove ZIP!!", "file", zip)
				return
			}
		}

		if errDir := os.RemoveAll(dir); errDir != nil {
			log.Error("Can't remove expanded dir", "error", errDir)
			return
		}
		log.Info("SUCCESS - Created CHD", "file", dir+".chd")
		convertedToCHD++
	}
	log.Info("Converted CHD files", "count", convertedToCHD, "CHD_Count", convertedToCHD, "ZIP_Count", len(zips))
	return nil
}
