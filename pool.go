package selenium_chrome_pool_go

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/tebeka/selenium"
)

// SeleniumChromePool manages a pool of Chrome instances.
type SeleniumChromePool struct {
	maxConnections int
	maxIdleTime    time.Duration
	maxTimeoutTime time.Duration
	headless       bool
	chromeArgs     []string // Use a slice of strings for Chrome arguments
	remoteURL      string
	proxy          string

	availableConnections chan selenium.WebDriver
	busyConnections      map[selenium.WebDriver]time.Time
	idleTimes            map[selenium.WebDriver]time.Time
	lock                 sync.Mutex
	cleanupDone          chan struct{}

	logger *log.Logger
}

// NewSeleniumChromePool creates a new SeleniumChromePool.
//
// Parameters:
//   - maxConnections:  Maximum number of Chrome instances in the pool.
//   - maxIdleTime: Maximum time a Chrome instance can be idle before being closed and replaced.
//   - maxTimeoutTime: Maximum time a Chrome instance can be used for a single task before being closed and replaced.
//   - headless: Whether to run Chrome in headless mode.
//   - chromeArgs: A slice of strings containing custom Chrome arguments (e.g., user-agent, extensions).
//   - remoteURL:  The URL of the Selenium Grid or remote ChromeDriver server (e.g., "http://localhost:4444/wd/hub").  If empty, a local ChromeDriver will be used.
//   - proxy: The address of a proxy server to use (e.g., "http://proxy.example.com:8080").
//   - logger:  A logger interface for logging messages.  If nil, the default logger is used.
//
// Returns:
//   - A pointer to a SeleniumChromePool instance, or an error if the pool cannot be initialized.
func NewSeleniumChromePool(maxConnections int, maxIdleTime time.Duration, maxTimeoutTime time.Duration, headless bool, chromeArgs []string, remoteURL string, proxy string, logger *log.Logger) (*SeleniumChromePool, error) {
	if logger == nil {
		logger = log.Default()
	}
	pool := &SeleniumChromePool{
		maxConnections:       maxConnections,
		maxIdleTime:          maxIdleTime,
		maxTimeoutTime:       maxTimeoutTime,
		headless:             headless,
		chromeArgs:           chromeArgs, // Store Chrome arguments
		remoteURL:            remoteURL,
		proxy:                proxy,
		availableConnections: make(chan selenium.WebDriver, maxConnections),
		busyConnections:      make(map[selenium.WebDriver]time.Time),
		idleTimes:            make(map[selenium.WebDriver]time.Time),
		logger:               logger,
		cleanupDone:          make(chan struct{}),
	}
	err := pool.initializePool()
	if err != nil {
		return nil, err
	}

	return pool, nil
}

func (p *SeleniumChromePool) createDriver() (selenium.WebDriver, error) {
	caps := selenium.Capabilities{"browserName": "chrome"}
	chromeCaps := map[string]interface{}{
		"goog:chromeOptions": map[string][]string{
			"args": {
				"--disable-gpu",
				"--disable-extensions",
				"--no-sandbox",
				"--disable-dev-shm-usage",
			},
		},
	}

	// Add headless argument if needed
	if p.headless {
		chromeCaps["goog:chromeOptions"].(map[string][]string)["args"] = append(chromeCaps["goog:chromeOptions"].(map[string][]string)["args"], "--headless=new")
	}

	// Add custom Chrome arguments
	chromeCaps["goog:chromeOptions"].(map[string][]string)["args"] = append(chromeCaps["goog:chromeOptions"].(map[string][]string)["args"], p.chromeArgs...)

	if p.proxy != "" {
		chromeCaps["goog:chromeOptions"].(map[string][]string)["args"] = append(chromeCaps["goog:chromeOptions"].(map[string][]string)["args"], fmt.Sprintf("--proxy-server=%s", p.proxy))
	}

	// Add Chrome options to capabilities
	for k, v := range chromeCaps {
		caps[k] = v
	}

	var wd selenium.WebDriver
	var err error // Declare err here

	if p.remoteURL != "" {
		wd, err = selenium.NewRemote(caps, p.remoteURL)
		if err != nil {
			p.logger.Printf("Failed to create remote WebDriver: %v\n", err)
			return nil, err
		}
		p.logger.Printf("Created remote Chrome Driver instance at: %s\n", p.remoteURL)
	} else {
		opts := []selenium.ServiceOption{}
		service, err := selenium.NewChromeDriverService("chromedriver", 9515, opts...)
		if err != nil {
			p.logger.Printf("Error starting ChromeDriver server: %v\n", err)
			return nil, err
		}
		defer service.Stop()

		wd, err = selenium.NewRemote(caps, fmt.Sprintf("http://localhost:%d", 9515))
		if err != nil {
			p.logger.Printf("Failed to create local WebDriver: %v\n", err)
			return nil, err
		}
		p.logger.Println("Created local Chrome Driver instance")
	}

	return wd, nil
}

func (p *SeleniumChromePool) initializePool() error {
	for i := 0; i < p.maxConnections; i++ {
		driver, err := p.createDriver()
		if err != nil {
			p.logger.Printf("Failed to create driver during pool initialization: %v\n", err)
			return err
		}
		p.availableConnections <- driver
		p.idleTimes[driver] = time.Now()
	}

	return nil
}

// GetConnection retrieves a Chrome WebDriver instance from the pool.
//
// Parameters:
//   - timeout: The maximum time to wait for a connection to become available.
//
// Returns:
//   - A selenium.WebDriver instance, or an error if a connection cannot be acquired within the timeout.
func (p *SeleniumChromePool) GetConnection(timeout time.Duration) (selenium.WebDriver, error) {
	select {
	case driver := <-p.availableConnections:
		p.lock.Lock()
		p.busyConnections[driver] = time.Now()
		delete(p.idleTimes, driver)
		p.lock.Unlock()
		p.logger.Printf("Acquired connection: Available %d, Busy %d\n", len(p.availableConnections), len(p.busyConnections))
		return driver, nil
	case <-time.After(timeout):
		p.logger.Println("Timeout waiting for connection")
		return nil, fmt.Errorf("timeout waiting for connection")
	}
}

// ReleaseConnection releases a Chrome WebDriver instance back to the pool.
//
// Parameters:
//   - driver: The selenium.WebDriver instance to release.
func (p *SeleniumChromePool) ReleaseConnection(driver selenium.WebDriver) {
	p.lock.Lock()
	if _, ok := p.busyConnections[driver]; ok {
		delete(p.busyConnections, driver)
		p.availableConnections <- driver
		p.idleTimes[driver] = time.Now()
		p.logger.Printf("Released connection: Available %d, Busy %d\n", len(p.availableConnections), len(p.busyConnections))
	} else {
		p.logger.Println("Attempted to release an unmanaged connection")
	}
	p.lock.Unlock()
}

// CloseConnection closes and destroys a Chrome WebDriver instance.
//
// Parameters:
//   - driver: The selenium.WebDriver instance to close.
func (p *SeleniumChromePool) CloseConnection(driver selenium.WebDriver) {
	err := driver.Quit()
	if err != nil {
		p.logger.Printf("Failed to close WebDriver: %v\n", err)
	} else {
		p.logger.Println("Closed WebDriver instance")
	}
}

func (p *SeleniumChromePool) cleanupIdleConnections(ctx context.Context) {
	ticker := time.NewTicker(p.maxIdleTime)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.lock.Lock()
			now := time.Now()
			idleDrivers := make([]selenium.WebDriver, 0)
			for driver, idleTime := range p.idleTimes {
				if now.Sub(idleTime) > p.maxIdleTime {
					idleDrivers = append(idleDrivers, driver)
				}
			}

			for _, driver := range idleDrivers {
				select {
				case d := <-p.availableConnections:
					if d != driver {
						p.availableConnections <- d
					}
					p.CloseConnection(driver)
					delete(p.idleTimes, driver)

					newDriver, err := p.createDriver()
					if err != nil {
						p.logger.Printf("Failed to create replacement driver: %v\n", err)
					} else {
						p.availableConnections <- newDriver
						p.idleTimes[newDriver] = time.Now()
						p.logger.Println("Created new driver to replenish pool")
					}
				default:
					p.logger.Printf("Driver %v not found in available connections during cleanup\n", driver)
				}
			}
			p.lock.Unlock()
		case <-ctx.Done():
			p.logger.Println("Stopping idle connection cleanup")
			return
		}
	}
}

func (p *SeleniumChromePool) cleanupTimeoutConnections(ctx context.Context) {
	ticker := time.NewTicker(p.maxTimeoutTime)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.lock.Lock()
			now := time.Now()
			timeoutDrivers := make([]selenium.WebDriver, 0)
			for driver, lastUsed := range p.busyConnections {
				if now.Sub(lastUsed) > p.maxTimeoutTime {
					timeoutDrivers = append(timeoutDrivers, driver)
				}
			}

			for _, driver := range timeoutDrivers {
				p.logger.Printf("Cleaning up timed-out connection (%.2f seconds)\n", time.Since(p.busyConnections[driver]).Seconds()) //used p.busyConnections[driver]
				delete(p.busyConnections, driver)
				p.CloseConnection(driver)
				delete(p.idleTimes, driver)

				newDriver, err := p.createDriver()
				if err != nil {
					p.logger.Printf("Failed to create replacement driver: %v\n", err)
				} else {
					p.availableConnections <- newDriver
					p.idleTimes[newDriver] = time.Now()
					p.logger.Println("Created new driver to replenish pool")
				}
			}
			p.lock.Unlock()
		case <-ctx.Done():
			p.logger.Println("Stopping timeout connection cleanup")
			return
		}
	}
}

// CloseAllConnections closes all connections in the pool.  It is important to call this when you are finished with the pool.
func (p *SeleniumChromePool) CloseAllConnections() {
	p.lock.Lock()
	defer p.lock.Unlock()

	// Close busy connections
	for driver := range p.busyConnections {
		p.CloseConnection(driver)
		delete(p.busyConnections, driver)
	}

	// Close available connections
	close(p.availableConnections)
	for driver := range p.availableConnections {
		p.CloseConnection(driver)
	}

	// Clear idleTimes
	for driver := range p.idleTimes {
		delete(p.idleTimes, driver)
	}

	p.logger.Println("All connections closed")
}

// StartCleanupThread starts the goroutines for cleaning up idle and timeout connections. It returns a context and a cancel function.
//
// It is important to call the cancel function when you are finished with the pool to stop the cleanup goroutines.
func (p *SeleniumChromePool) StartCleanupThread() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())

	go p.cleanupIdleConnections(ctx)
	go p.cleanupTimeoutConnections(ctx)

	p.logger.Println("Started cleanup goroutines")
	return ctx, cancel
}

func (p *SeleniumChromePool) isDriverAvailable(driver selenium.WebDriver) bool {
	// Difficult to implement reliably with channels; omit for now.
	return true
}

// HandleInterrupt signals for graceful shutdown
// this function should be called in main function
// Parameters:
//   - pool: A pointer to a SeleniumChromePool instance.
//   - cancel: The cancel function returned by StartCleanupThread.
//   - logger:  A logger interface for logging messages.
func HandleInterrupt(pool *SeleniumChromePool, cancel context.CancelFunc, logger *log.Logger) {

	// Handle OS signals for graceful shutdown
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-signalChan
		logger.Printf("Received signal: %v.  Shutting down...\n", sig)
		cancel()                   // Signal cleanup goroutines to stop
		pool.CloseAllConnections() // Close all WebDriver connections
		os.Exit(0)                 // Exit the program
	}()
}
