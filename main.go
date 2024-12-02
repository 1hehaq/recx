package main

import (
	"bufio"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

const (
	reflectionMarker = "reflect_test_parameter"
	maxWorkers       = 200
	timeout          = 10 * time.Second
	bufferSize       = 50000
	maxRedirects     = 3
	maxDepth         = 5
	maxURLsPerDomain = 10000
	scanTimeout      = 60 * time.Second
	minScanTime      = 5 * time.Second
	workerCount      = 40
	specialChars     = "'<>$|()`;{}"
)

var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:89.0) Gecko/20100101 Firefox/89.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/14.1.1 Safari/605.1.15",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Edge/91.0.864.59",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 14_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/14.1.1 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (iPad; CPU OS 14_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/14.0 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (Android 11; Mobile; rv:68.0) Gecko/68.0 Firefox/88.0",
	"Mozilla/5.0 (Windows NT 10.0; WOW64; Trident/7.0; rv:11.0) like Gecko",
	"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
}

const usage = `usage: recx [options]

crawler for finding reflected parameters!
version: v1.0

options:
  -h, -help    show help message
  -v           show version

use cases:
  echo "example.com" | recx
  cat urls.txt | recx
  subfinder -d example.com | recx | nuclei -t xss-reflected.yaml
`

type Crawler struct {
	client        *http.Client
	visitedURLs   sync.Map
	parameters    sync.Map
	targetDomain  string
	semaphore     chan struct{}
	urlCount      int32
	depth         int32
	urlCountMutex sync.Mutex
}

func newCrawler(targetURL string) *Crawler {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: time.Second,
			DualStack: true,
		}).DialContext,
		MaxIdleConns:        500,
		MaxIdleConnsPerHost: 100,
		MaxConnsPerHost:     100,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  true,
		DisableKeepAlives:   false,
		ForceAttemptHTTP2:   true,
	}

	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	parsedURL, _ := url.Parse(targetURL)
	return &Crawler{
		client:       client,
		targetDomain: parsedURL.Host,
		semaphore:    make(chan struct{}, maxWorkers),
	}
}

func (c *Crawler) isSameDomain(urlStr string) bool {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return false
	}
	return strings.HasSuffix(parsed.Host, c.targetDomain)
}

func (c *Crawler) fetchURL(targetURL string) (string, error) {
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return "", err
	}

	// Randomly select a user agent
	req.Header.Set("User-Agent", userAgents[rand.Intn(len(userAgents))])
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Connection", "keep-alive")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch url: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return "", fmt.Errorf("failed to read response: %v", err)
	}

	return string(body), nil
}

func (c *Crawler) extractLinks(content string) []string {
	links := make([]string, 0, 100)
	seen := make(map[string]bool)
	tokenizer := html.NewTokenizer(strings.NewReader(content))

	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			break
		}

		if tokenType == html.StartTagToken {
			token := tokenizer.Token()
			if token.Data == "a" {
				for _, attr := range token.Attr {
					if attr.Key == "href" {
						if !seen[attr.Val] {
							seen[attr.Val] = true
							links = append(links, attr.Val)
						}
						break
					}
				}
			}
		}
	}
	return links
}

func (c *Crawler) shouldCrawl() bool {
	c.urlCountMutex.Lock()
	defer c.urlCountMutex.Unlock()

	if c.urlCount >= maxURLsPerDomain {
		return false
	}
	c.urlCount++
	return true
}

func (c *Crawler) crawlPage(pageURL string, paramChan chan<- string, depth int) {
	if depth > maxDepth {
		return
	}

	if !c.shouldCrawl() {
		return
	}

	c.semaphore <- struct{}{}
	defer func() { <-c.semaphore }()

	if _, visited := c.visitedURLs.LoadOrStore(pageURL, true); visited {
		return
	}

	content, err := c.fetchURL(pageURL)
	if err != nil {
		return
	}

	if parsedURL, err := url.Parse(pageURL); err == nil {
		params := parsedURL.Query()
		for param := range params {
			select {
			case paramChan <- param:
			default:
			}
		}
	}

	links := c.extractLinks(content)
	var wg sync.WaitGroup
	for _, href := range links {
		fullURL, err := url.Parse(href)
		if err != nil {
			continue
		}

		absoluteURL := fullURL.String()
		if c.isSameDomain(absoluteURL) {
			wg.Add(1)
			go func(url string) {
				defer wg.Done()
				c.crawlPage(url, paramChan, depth+1)
			}(absoluteURL)
		}

		if fullURL.RawQuery != "" {
			params := fullURL.Query()
			for param := range params {
				select {
				case paramChan <- param:
				default:
				}
			}
		}
	}
	wg.Wait()
}

func (c *Crawler) checkUnfilteredChars(baseURL, param string) []string {
	var unfilteredChars []string
	randBytes := make([]byte, 8)

	for _, char := range specialChars {
		rand.Read(randBytes)
		marker := fmt.Sprintf("px%x%c%x", randBytes[:4], char, randBytes[4:])
		testURL := fmt.Sprintf("%s?%s=%s", baseURL, param, marker)

		content, err := c.fetchURL(testURL)
		if err != nil {
			continue
		}

		if strings.Contains(content, marker) {
			unfilteredChars = append(unfilteredChars, string(char))
		}
	}

	return unfilteredChars
}

func (c *Crawler) checkReflection(baseURL, param string) {
	randBytes := make([]byte, 12)
	rand.Read(randBytes)
	uniqueMarker := fmt.Sprintf("%x", randBytes)
	testURL := fmt.Sprintf("%s?%s=%s", baseURL, param, uniqueMarker)

	content, err := c.fetchURL(testURL)
	if err != nil {
		return
	}

	if strings.Contains(content, uniqueMarker) {
		rand.Read(randBytes)
		verifyMarker := fmt.Sprintf("%x", randBytes)
		verifyURL := fmt.Sprintf("%s?%s=%s", baseURL, param, verifyMarker)

		verifyContent, err := c.fetchURL(verifyURL)
		if err != nil {
			return
		}

		if strings.Contains(verifyContent, verifyMarker) {
			if unfilteredChars := c.checkUnfilteredChars(baseURL, param); len(unfilteredChars) > 0 {
				result := fmt.Sprintf("%s?%s=REFLECTED (unfiltered:%s )", baseURL, param, strings.Join(unfilteredChars, ""))
				fmt.Println(result)
			}
		}
	}
}

func isValidReflectionContext(content, marker string) bool {
	idx := strings.Index(content, marker)
	if idx == -1 {
		return false
	}

	start := max(0, idx-100)
	end := min(len(content), idx+len(marker)+100)
	context := content[start:end]

	if strings.Contains(context, "href=") || strings.Contains(context, "src=") {
		return false
	}

	if strings.Contains(strings.ToLower(context), "<script") {
		return false
	}

	if strings.Contains(strings.ToLower(context), "<meta") {
		return false
	}

	if strings.Contains(context, "<!--") || strings.Contains(context, "-->") {
		return false
	}

	return true
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func showUsageAndExit(err string) {
	fmt.Fprintf(os.Stderr, "error: %s\n\n", err)
	fmt.Println(usage)
	os.Exit(1)
}

func processURL(targetURL string) {
	if targetURL == "" {
		return
	}

	if !strings.HasPrefix(targetURL, "https://") && !strings.HasPrefix(targetURL, "http://") {
		targetURL = "https://" + targetURL
	}

	_, err := url.Parse(targetURL)
	if err != nil {
		showUsageAndExit(fmt.Sprintf("invalid url: %s", targetURL))
	}

	crawler := newCrawler(targetURL)
	paramChan := make(chan string, bufferSize)

	crawlComplete := make(chan bool)
	go func() {
		crawler.crawlPage(targetURL, paramChan, 0)
		crawlComplete <- true
	}()

	var wg sync.WaitGroup
	processedParams := make(map[string]bool, 1000)
	paramMutex := &sync.Mutex{}

	workersDone := make(chan bool)
	go func() {
		for i := 0; i < maxWorkers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for param := range paramChan {
					paramMutex.Lock()
					if !processedParams[param] {
						processedParams[param] = true
						paramMutex.Unlock()
						crawler.checkReflection(targetURL, param)
					} else {
						paramMutex.Unlock()
					}
				}
			}()
		}
		wg.Wait()
		workersDone <- true
	}()

	startTime := time.Now()
	select {
	case <-time.After(scanTimeout):
		fmt.Fprintf(os.Stderr, "timeout reached for: %s\n", targetURL)
	case <-crawlComplete:
		if elapsed := time.Since(startTime); elapsed < minScanTime {
			time.Sleep(minScanTime - elapsed)
		}
		close(paramChan)
		<-workersDone
	}
}

func main() {
	help := flag.Bool("help", false, "show help message")
	h := flag.Bool("h", false, "show help message")
	version := flag.Bool("v", false, "show version")
	flag.Parse()

	if *help || *h {
		fmt.Println(usage)
		os.Exit(0)
	}

	if *version {
		fmt.Println("recx version 1.0")
		os.Exit(0)
	}

	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		showUsageAndExit("no input provided")
	}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	urlCount := 0
	for scanner.Scan() {
		targetURL := strings.TrimSpace(scanner.Text())
		if targetURL != "" {
			urlCount++
			processURL(targetURL)
		}
	}

	if err := scanner.Err(); err != nil {
		showUsageAndExit(fmt.Sprintf("error reading input: %v", err))
	}

	if urlCount == 0 {
		showUsageAndExit("no valid urls provided")
	}
}
