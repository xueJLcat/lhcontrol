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
	// closed marks a deliberate Close, distinguishing it from a transient
	// failure that nulled file; only the former permanently stops writes, so a
	// failed reopen retries on the next write instead of silently disabling
	// file logging for the rest of the session.
	closed bool
	// rotationDropped counts bytes discarded while the backup could not be
	// published. Because an oversized log makes every subsequent write retry
	// the rotation immediately, failures are always consecutive. Budgeting
	// by dropped bytes (instead of failure count) keeps a short-lived
	// backup lock from sacrificing the whole log: in-place truncation only
	// happens after discarding has already lost as much data as the
	// truncation itself would drop.
	rotationDropped int64
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
	if err == nil && info.Size() > maxSize {
		if rotateErr := writer.rotateClosedFile(); rotateErr != nil {
			// A transient problem with the backup target must not disable
			// file logging for the whole session. Keep the oversized file;
			// the writer retries the rotation on the next write.
			log.Printf("Deferring diagnostic log rotation after startup failure: %v", rotateErr)
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
	if err := os.Remove(writer.path); err != nil && !os.IsNotExist(err) {
		// The archived tail is already published. Blank the original in
		// place so the next rotation does not re-archive the same bytes;
		// report the failure only when neither removal works.
		if truncated, truncErr := os.OpenFile(writer.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600); truncErr == nil {
			_ = truncated.Close()
			return nil
		}
		return fmt.Errorf("remove rotated diagnostic log: %w", err)
	}
	return nil
}

// logFileClose releases an open log file handle. Injectable in tests.
var logFileClose = func(file *os.File) error { return file.Close() }

// closeErrLogger reports rotation failures without routing through the global
// logger: in production that logger writes into this very rotating file, and
// logging while the writer mutex is held would re-enter the non-reentrant
// mutex and deadlock the whole logging subsystem.
var closeErrLogger = log.New(os.Stderr, "", log.LstdFlags)

// closeFileBestEffort drops the current log handle even when Close reports an
// error. A handle whose Close failed is unusable anyway; keeping it would make
// every later rotation retry the same failing Close and permanently break
// file logging for the session.
func (writer *rotatingLogFile) closeFileBestEffort() {
	if writer.file == nil {
		return
	}
	if err := logFileClose(writer.file); err != nil {
		closeErrLogger.Printf("Closing the diagnostic log failed; continuing rotation: %v", err)
	}
	writer.file = nil
}

func (writer *rotatingLogFile) rotateLocked() error {
	writer.closeFileBestEffort()
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

// truncateLocked enforces the size cap in place when the backup cannot be
// produced (for example because the backup file is locked by another
// process). Oldest content is dropped, but the log stays bounded.
func (writer *rotatingLogFile) truncateLocked() error {
	writer.closeFileBestEffort()
	file, err := os.OpenFile(writer.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	writer.file = file
	writer.size = 0
	notice := "lhcontrol: log rotated in place after repeated backup failures; older entries were dropped\n"
	written, _ := writer.file.Write([]byte(notice))
	writer.size += int64(written)
	return nil
}

// reopenLocked restores a dropped log handle after a failed rotation so file
// logging recovers on the next write instead of staying disabled. It does not
// reset rotationDropped, keeping the in-place-truncation budget intact.
func (writer *rotatingLogFile) reopenLocked() error {
	file, err := os.OpenFile(writer.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	writer.file = file
	if info, statErr := file.Stat(); statErr == nil {
		writer.size = info.Size()
	} else {
		// An unknown size must not disable the cap: assume the worst case so
		// the next write re-enters the rotation check instead of letting the
		// file grow unbounded until the tracked size catches up.
		writer.size = writer.maxSize
	}
	return nil
}

func (writer *rotatingLogFile) Write(value []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	if writer.file == nil {
		if writer.closed {
			return 0, os.ErrClosed
		}
		if err := writer.reopenLocked(); err != nil {
			return 0, err
		}
	}
	originalLength := len(value)
	if int64(len(value)) > writer.maxSize {
		value = value[len(value)-int(writer.maxSize):]
	}
	if writer.size > 0 && writer.size+int64(len(value)) > writer.maxSize {
		if err := writer.rotateLocked(); err != nil {
			writer.rotationDropped += int64(len(value))
			if writer.rotationDropped < writer.maxSize {
				return 0, err
			}
			if truncateErr := writer.truncateLocked(); truncateErr != nil {
				return 0, errors.Join(err, fmt.Errorf("fallback truncation: %w", truncateErr))
			}
			writer.rotationDropped = 0
		} else {
			writer.rotationDropped = 0
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
		if writer.closed {
			return os.ErrClosed
		}
		if err := writer.reopenLocked(); err != nil {
			return err
		}
	}
	return writer.file.Sync()
}

func (writer *rotatingLogFile) Close() error {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	writer.closed = true
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
		// Stderr first: MultiWriter stops at the first failing writer, so the
		// console mirror still receives a message even when the rotating file
		// write fails (precisely the situation diagnostics must not lose).
		log.SetOutput(io.MultiWriter(os.Stderr, logFile))
	} else {
		log.SetOutput(logFile)
	}

	log.Println("-----------------------------------------")
	log.Printf("Diagnostic log: %s", logFilePath)
	log.Println("-----------------------------------------")

	return logFile, nil
}

func main() {
	// Define command-line flag for logging. Parse with ContinueOnError so an
	// unknown argument (for example one appended by a shortcut or shell
	// association) cannot terminate the second instance with exit code 2
	// before it reaches the single-instance check that focuses the running
	// window.
	flagSet := flag.NewFlagSet("lhcontrol", flag.ContinueOnError)
	mirrorConsole := flagSet.Bool("log", false, "Also mirror diagnostics to the console")
	if parseErr := flagSet.Parse(os.Args[1:]); parseErr != nil {
		log.Printf("Ignoring unrecognized command line arguments: %v", parseErr)
	}

	// Setup standard logger flags (applies to console and potentially file)
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	// Acquire the single-instance lock before opening (and possibly rotating)
	// the diagnostic log. Otherwise a second instance could rotate or delete
	// the running instance's active log file. On the already-running path the
	// log file is never needed, so these messages go to the default logger.
	releaseInstance, alreadyRunning, err := platform.AcquireSingleInstance(instanceMutexName)
	if err != nil {
		log.Printf("FATAL: Failed to acquire the application instance lock: %v", err)
		os.Exit(1)
	}
	if alreadyRunning {
		log.Println("Application is already running. Bringing existing window to front...")
		if !platform.BringWindowToFront(appTitle) {
			log.Printf("Existing lhcontrol process was detected, but its window was not found")
			os.Exit(1)
		}
		return
	}
	defer releaseInstance()

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
		BackgroundColour: &options.RGBA{R: 244, G: 246, B: 252, A: 1},
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
