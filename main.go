package main

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"

	"lhcontrol/internal/platform"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

const appTitle = "lhcontrol"
const instanceMutexName = `Local\FlameInTheDark.LighthouseControl`

const maxLogSize = 5 * 1024 * 1024

type rotatingLogFile struct {
	mutex      sync.Mutex
	path       string
	backupPath string
	maxSize    int64
	file       *os.File
	size       int64
}

func openRotatingLogFile(path string, maxSize int64) (*rotatingLogFile, error) {
	if maxSize <= 0 {
		return nil, fmt.Errorf("maximum log size must be positive")
	}
	writer := &rotatingLogFile{
		path:       path,
		backupPath: path + ".1",
		maxSize:    maxSize,
	}
	info, err := os.Stat(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil && info.Size() >= maxSize {
		if err := writer.rotateClosedFile(); err != nil {
			return nil, err
		}
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	writer.file = file
	if info, err = file.Stat(); err != nil {
		_ = file.Close()
		return nil, err
	}
	writer.size = info.Size()
	return writer, nil
}

func (writer *rotatingLogFile) rotateClosedFile() error {
	info, err := os.Stat(writer.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	source, err := os.Open(writer.path)
	if err != nil {
		return err
	}
	if info.Size() > writer.maxSize {
		if _, err := source.Seek(-writer.maxSize, io.SeekEnd); err != nil {
			_ = source.Close()
			return err
		}
	}
	temp, err := os.CreateTemp(filepath.Dir(writer.backupPath), filepath.Base(writer.backupPath)+".tmp-*")
	if err != nil {
		_ = source.Close()
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0600); err != nil {
		_ = source.Close()
		_ = temp.Close()
		return err
	}
	if _, err := io.Copy(temp, source); err != nil {
		_ = source.Close()
		_ = temp.Close()
		return err
	}
	if err := source.Close(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Remove(writer.backupPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove previous log backup: %w", err)
	}
	if err := os.Rename(tempPath, writer.backupPath); err != nil {
		return fmt.Errorf("publish diagnostic log backup: %w", err)
	}
	return os.Remove(writer.path)
}

func (writer *rotatingLogFile) rotateLocked() error {
	if writer.file != nil {
		if err := writer.file.Close(); err != nil {
			return err
		}
		writer.file = nil
	}
	if err := writer.rotateClosedFile(); err != nil {
		file, reopenErr := os.OpenFile(writer.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if reopenErr == nil {
			writer.file = file
			if info, statErr := file.Stat(); statErr == nil {
				writer.size = info.Size()
			}
		}
		if reopenErr != nil {
			return errors.Join(err, fmt.Errorf("reopen diagnostic log after rotation failure: %w", reopenErr))
		}
		return err
	}
	file, err := os.OpenFile(writer.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	writer.file = file
	writer.size = 0
	return nil
}

func (writer *rotatingLogFile) Write(value []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	if writer.file == nil {
		return 0, os.ErrClosed
	}
	originalLength := len(value)
	if int64(len(value)) > writer.maxSize {
		value = value[len(value)-int(writer.maxSize):]
	}
	if writer.size > 0 && writer.size+int64(len(value)) > writer.maxSize {
		if err := writer.rotateLocked(); err != nil {
			return 0, err
		}
	}
	written, err := writer.file.Write(value)
	writer.size += int64(written)
	if err != nil {
		return written, err
	}
	if written != len(value) {
		return written, io.ErrShortWrite
	}
	return originalLength, nil
}

func (writer *rotatingLogFile) Sync() error {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	if writer.file == nil {
		return os.ErrClosed
	}
	return writer.file.Sync()
}

func (writer *rotatingLogFile) Close() error {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	if writer.file == nil {
		return nil
	}
	err := writer.file.Close()
	writer.file = nil
	return err
}

// setupLogging always keeps a bounded diagnostic log in the user config
// directory. Production GUI builds have no reliable console, so file logging
// preserves the evidence needed to diagnose an intermittent crash.
func setupLogging(mirrorConsole bool) (*rotatingLogFile, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	logDir := filepath.Join(configDir, "lhcontrol")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, err
	}
	logFilePath := filepath.Join(logDir, "lhcontrol.log")

	logFile, err := openRotatingLogFile(logFilePath, maxLogSize)
	if err != nil {
		return nil, err
	}

	if mirrorConsole {
		log.SetOutput(io.MultiWriter(logFile, os.Stderr))
	} else {
		log.SetOutput(logFile)
	}

	log.Println("-----------------------------------------")
	log.Printf("Diagnostic log: %s", logFilePath)
	log.Println("-----------------------------------------")

	return logFile, nil
}

func main() {
	// Define command-line flag for logging
	mirrorConsole := flag.Bool("log", false, "Also mirror diagnostics to the console")
	flag.Parse() // Parse command line arguments

	// Setup standard logger flags (applies to console and potentially file)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	logFile, errLog := setupLogging(*mirrorConsole)
	if errLog != nil {
		log.Printf("Error setting up diagnostic logging, continuing without a file: %v", errLog)
		logFile = nil
	} else {
		defer func() {
			log.Println("Closing log file handle...")
			_ = logFile.Sync()
			_ = logFile.Close()
		}()
	}

	releaseInstance, alreadyRunning, err := platform.AcquireSingleInstance(instanceMutexName)
	if err != nil {
		log.Printf("FATAL: Failed to acquire the application instance lock: %v", err)
		if logFile != nil {
			_ = logFile.Sync()
		}
		os.Exit(1)
	}
	if alreadyRunning {
		log.Println("Application is already running. Bringing existing window to front...")
		if !platform.BringWindowToFront(appTitle) {
			log.Printf("Existing lhcontrol process was detected, but its window was not found")
			if logFile != nil {
				_ = logFile.Sync()
			}
			os.Exit(1)
		}
		if logFile != nil {
			_ = logFile.Sync()
		}
		return
	}
	defer releaseInstance()
	log.Printf("Acquired Windows instance mutex %s", instanceMutexName)

	// Create app
	app := NewApp()

	err = wails.Run(&options.App{
		Title:         appTitle, // Use constant
		Width:         512,
		Height:        800,
		MinWidth:      440,
		MinHeight:     560,
		DisableResize: false,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		log.Println("FATAL: Error running Wails app: ", err.Error())
		if logFile != nil {
			logFile.Sync()
		} // Sync before exit, only if file exists
		os.Exit(1)
	}
	log.Println("Application exited cleanly.")
	// Sync on clean exit is handled by the defer if logFile != nil
}
