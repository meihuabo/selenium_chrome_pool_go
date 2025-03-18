# Selenium Chrome Pool Usage Example

This example demonstrates how to use the `seleniumchromepool` library to manage a pool of Chrome instances for automated web testing or scraping.

## Prerequisites

*   Go installed (version 1.16 or later)
*   ChromeDriver installed and in your system's PATH (if using local ChromeDriver)
*   A Selenium Grid or remote ChromeDriver server running (if using a remote connection)
*   The `tebeka/selenium` Go package: `go get github.com/tebeka/selenium`

## Example Code (main.go)

```go
package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"time"

	"your_module_path/seleniumchromepool" // Replace with your actual module path
	"github.com/tebeka/selenium"
)

func main() {
	// 1. Configure the logger
	logger := log.New(os.Stdout, "SCP: ", log.LstdFlags|log.Lshortfile)

	// 2. Define pool parameters
	maxConnections := 3
	maxIdleTime := seleniumchromepool.parseDuration("30s")
	maxTimeoutTime := seleniumchromepool.parseDuration("300s")
	headless := false // Set to true for headless browsing
	chromeArgs := []string{
		"--user-agent=Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36", // Example user-agent
		// Add other Chrome arguments as needed
	}
	remoteURL := "http://192.168.2.117:4450/wd/hub" // Replace with your Selenium Grid URL, or leave empty for local ChromeDriver
	proxy := ""                                         // Example proxy server address

	// 3. Create the Selenium Chrome Pool
	pool, err := seleniumchromepool.NewSeleniumChromePool(maxConnections, maxIdleTime, maxTimeoutTime, headless, chromeArgs, remoteURL, proxy, logger)
	if err != nil {
		logger.Fatalf("Failed to create pool: %v", err)
	}
	defer pool.CloseAllConnections() // Ensure the pool is closed when the program exits

	// 4. Start the cleanup goroutines
	cleanupCtx, cleanupCancel := pool.StartCleanupThread()
	defer cleanupCancel() // Ensure the cleanup goroutines are stopped when the program exits

	// 5. Handle interrupt signals for graceful shutdown
	seleniumchromepool.HandleInterrupt(pool, cleanupCancel, logger)

	// 6. Acquire and use connections from the pool
	for i := 0; i < 5; i++ {
		// a. Get a connection
		driver, err := pool.GetConnection(time.Second * 10)
		if err != nil {
			logger.Printf("Failed to get connection: %v", err)
			time.Sleep(time.Second) // Wait before retrying
			continue                // Skip to the next iteration
		}
		defer pool.ReleaseConnection(driver) // Ensure the connection is released back to the pool

		// b. Use the driver to perform Selenium actions
		if err := driver.Get("https://www.example.com"); err != nil {
			logger.Printf("Failed to get page: %v", err)
			continue // Or handle the error as appropriate
		}
		title, err := driver.Title()
		if err != nil {
			logger.Printf("Failed to get title: %v", err)
			continue // Or handle the error as appropriate
		}

		u, err := url.Parse("https://www.example.com")
		if err != nil {
			logger.Fatalf("Failed to parse URL: %v", err)
		}

		fmt.Printf("URL host: %s\n", u.Host)
		fmt.Printf("Page title: %s\n", title)

		time.Sleep(time.Second * 5) // Simulate some work
	}

	// 7.  cleanupCtx.Done() is no longer waited upon.  The HandleInterrupt and defer ensure graceful shutdown
	logger.Println("Finished using the pool.  The program will now exit gracefully.")
}

// Helper function to parse duration strings
func parseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		panic(err)
	}
	return d
}