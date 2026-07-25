package main

import (
	"embed"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"

	"lhcontrol/internal/platform"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

const lockPort = "34115"     // Port used for single instance check
const appTitle = "lhcontrol" // Define app title constant

const maxLogSize = 5 * 1024 * 1024

// setupLogging always keeps a bounded diagnostic log in the user config
// directory. Production GUI builds have no reliable console, so opt-in file
// logging loses the evidence needed to diagnose an intermittent crash.
func setupLogging(mirrorConsole bool) (*os.File, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	logDir := filepath.Join(configDir, "lhcontrol")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, err
	}
	logFilePath := filepath.Join(logDir, "lhcontrol.log")

	openFlags := os.O_APPEND | os.O_CREATE | os.O_WRONLY
	if info, statErr := os.Stat(logFilePath); statErr == nil && info.Size() >= maxLogSize {
		openFlags = os.O_TRUNC | os.O_CREATE | os.O_WRONLY
	}
	logFile, err := os.OpenFile(logFilePath, openFlags, 0600)
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

	// Attempt to acquire the instance lock
	lockAddr := fmt.Sprintf("127.0.0.1:%s", lockPort)
	listener, err := net.Listen("tcp", lockAddr)
	if err != nil {
		if strings.Contains(err.Error(), "address already in use") || strings.Contains(err.Error(), "bind: address already in use") || strings.Contains(err.Error(), "bind: Only one usage of each socket address") {
			log.Println("Application is already running. Bringing existing window to front...")
			if !platform.BringWindowToFront(appTitle) {
				log.Printf("FATAL: port %s is occupied, but no lhcontrol window was found", lockPort)
				if logFile != nil {
					_ = logFile.Sync()
				}
				os.Exit(1)
			}
			if logFile != nil {
				logFile.Sync()
			} // Sync before exit, only if file exists
			os.Exit(0)
		} else {
			log.Printf("FATAL: Failed to acquire instance lock on port %s: %v", lockPort, err)
			if logFile != nil {
				logFile.Sync()
			} // Sync before exit, only if file exists
			os.Exit(1)
		}
	}
	defer listener.Close()
	log.Printf("Acquired instance lock on port %s", lockPort)

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
