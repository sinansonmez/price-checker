package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"
)

const (
	defaultDataPath = "prices.csv"
)

var defaultURLs = []string{
	"https://www.mediaworld.it/it/product/_electrolux-ees68600l-596-cm-classe-a-323369.html",
	"https://www.mediaworld.it/it/product/_bosch-smi6ecs12e-598-cm-classe-a-400229.html",
}

var errNoPrevious = errors.New("no previous price")

type priceResult struct {
	URL       string
	Price     float64
	Currency  string
	CheckedAt time.Time
	Delta     *float64
	Decreased bool
	Err       error
}

func main() {
	urlsFlag := flag.String("urls", "", "Comma-separated list of product URLs to check")
	urlsFile := flag.String("urls-file", "", "Path to a file containing URLs (one per line)")
	dataPath := flag.String("data", defaultDataPath, "Path to the CSV data file")
	runOnce := flag.Bool("run-once", false, "Run a single check and exit")
	interval := flag.Duration("interval", 24*time.Hour, "Interval between checks")
	flag.Parse()

	urls, err := loadURLs(*urlsFlag, *urlsFile)
	if err != nil {
		log.Fatalf("failed to load URLs: %v", err)
	}
	if len(urls) == 0 {
		urls = append([]string{}, defaultURLs...)
	}

	if err := ensureCSV(*dataPath); err != nil {
		log.Fatalf("failed to initialize data file: %v", err)
	}

	for {
		summary, results := runCheck(*dataPath, urls)
		printResults(results)

		if err := sendTelegramSummary(summary); err != nil {
			log.Printf("telegram summary error: %v", err)
		}

		if *runOnce {
			break
		}

		time.Sleep(*interval)
	}
}

func loadURLs(urlsFlag, urlsFile string) ([]string, error) {
	if urlsFile != "" {
		data, err := os.ReadFile(urlsFile)
		if err != nil {
			return nil, err
		}
		var urls []string
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			urls = append(urls, line)
		}
		return urls, nil
	}

	if urlsFlag == "" {
		return nil, nil
	}

	parts := strings.Split(urlsFlag, ",")
	urls := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			urls = append(urls, trimmed)
		}
	}
	return urls, nil
}

func ensureCSV(path string) error {
	_, err := os.Stat(path)
	if err == nil {
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return err
	}

	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	if err := writer.Write([]string{"checked_at", "url", "price", "currency", "delta", "decreased"}); err != nil {
		return err
	}
	writer.Flush()
	return writer.Error()
}

func runCheck(dataPath string, urls []string) (string, []priceResult) {
	results := make([]priceResult, 0, len(urls))
	for _, url := range urls {
		results = append(results, checkURL(dataPath, url))
	}

	summary := buildSummary(results)
	return summary, results
}

func checkURL(dataPath, url string) priceResult {
	checkedAt := time.Now().UTC()
	price, currency, err := fetchPrice(url)
	if err != nil {
		return priceResult{URL: url, CheckedAt: checkedAt, Err: err}
	}

	previousPrice, err := latestPriceFromCSV(dataPath, url)
	if err != nil && !errors.Is(err, errNoPrevious) {
		return priceResult{URL: url, Price: price, Currency: currency, CheckedAt: checkedAt, Err: err}
	}

	var delta *float64
	decreased := false
	if err == nil {
		deltaValue := price - previousPrice
		if math.Abs(deltaValue) > 0.0001 {
			delta = &deltaValue
			if deltaValue < 0 {
				decreased = true
			}
		}
	}

	if err := appendPriceToCSV(dataPath, priceResult{
		URL:       url,
		Price:     price,
		Currency:  currency,
		CheckedAt: checkedAt,
		Delta:     delta,
		Decreased: decreased,
	}); err != nil {
		return priceResult{URL: url, Price: price, Currency: currency, CheckedAt: checkedAt, Err: err}
	}

	return priceResult{
		URL:       url,
		Price:     price,
		Currency:  currency,
		CheckedAt: checkedAt,
		Delta:     delta,
		Decreased: decreased,
	}
}

func latestPriceFromCSV(path, url string) (float64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	reader := csv.NewReader(bufio.NewReader(file))
	_, err = reader.Read() // header
	if err != nil {
		return 0, err
	}

	var lastPrice float64
	found := false
	for {
		record, err := reader.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return 0, err
		}
		if len(record) < 6 {
			continue
		}
		if record[1] != url {
			continue
		}
		parsed, err := parsePrice(record[2])
		if err != nil {
			continue
		}
		lastPrice = parsed
		found = true
	}

	if !found {
		return 0, errNoPrevious
	}
	return lastPrice, nil
}

func appendPriceToCSV(path string, result priceResult) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	delta := ""
	if result.Delta != nil {
		delta = fmt.Sprintf("%.2f", *result.Delta)
	}
	decreased := "0"
	if result.Decreased {
		decreased = "1"
	}

	if err := writer.Write([]string{
		result.CheckedAt.Format(time.RFC3339),
		result.URL,
		fmt.Sprintf("%.2f", result.Price),
		result.Currency,
		delta,
		decreased,
	}); err != nil {
		return err
	}
	writer.Flush()
	return writer.Error()
}

func fetchPrice(url string) (float64, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; PriceChecker/1.0)")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, "", fmt.Errorf("unexpected status %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, "", err
	}
	content := string(body)

	if price, currency, ok := extractPriceFromMeta(content); ok {
		return price, currency, nil
	}

	if price, currency, ok := extractPriceFromJSONLD(content); ok {
		return price, currency, nil
	}

	return 0, "", errors.New("price not found")
}

func extractPriceFromMeta(html string) (float64, string, bool) {
	metaPatterns := []struct {
		pattern  *regexp.Regexp
		currency string
	}{
		{pattern: regexp.MustCompile(`(?i)<meta[^>]+itemprop=["']price["'][^>]+content=["']([^"']+)["']`)},
		{pattern: regexp.MustCompile(`(?i)<meta[^>]+property=["']product:price:amount["'][^>]+content=["']([^"']+)["']`)},
		{pattern: regexp.MustCompile(`(?i)<meta[^>]+property=["']og:price:amount["'][^>]+content=["']([^"']+)["']`)},
	}

	currency := extractMetaContent(html, regexp.MustCompile(`(?i)<meta[^>]+property=["']product:price:currency["'][^>]+content=["']([^"']+)["']`))
	if currency == "" {
		currency = extractMetaContent(html, regexp.MustCompile(`(?i)<meta[^>]+property=["']og:price:currency["'][^>]+content=["']([^"']+)["']`))
	}
	if currency == "" {
		currency = "EUR"
	}

	for _, meta := range metaPatterns {
		match := meta.pattern.FindStringSubmatch(html)
		if len(match) < 2 {
			continue
		}
		price, err := parsePrice(match[1])
		if err != nil {
			continue
		}
		return price, currency, true
	}

	return 0, "", false
}

func extractMetaContent(html string, pattern *regexp.Regexp) string {
	match := pattern.FindStringSubmatch(html)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func extractPriceFromJSONLD(html string) (float64, string, bool) {
	scriptPattern := regexp.MustCompile(`(?is)<script[^>]+type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
	matches := scriptPattern.FindAllStringSubmatch(html, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		data := strings.TrimSpace(match[1])
		if data == "" {
			continue
		}
		var payload interface{}
		decoder := json.NewDecoder(strings.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&payload); err != nil {
			continue
		}
		if price, currency, ok := findPriceInJSONLD(payload); ok {
			return price, currency, true
		}
	}
	return 0, "", false
}

func findPriceInJSONLD(payload interface{}) (float64, string, bool) {
	switch value := payload.(type) {
	case map[string]interface{}:
		if price, currency, ok := priceFromMap(value); ok {
			return price, currency, true
		}
		for _, v := range value {
			if price, currency, ok := findPriceInJSONLD(v); ok {
				return price, currency, true
			}
		}
	case []interface{}:
		for _, item := range value {
			if price, currency, ok := findPriceInJSONLD(item); ok {
				return price, currency, true
			}
		}
	}
	return 0, "", false
}

func priceFromMap(data map[string]interface{}) (float64, string, bool) {
	priceValue, ok := data["price"]
	if ok {
		price, err := parsePrice(fmt.Sprint(priceValue))
		if err == nil {
			currency := "EUR"
			if c, ok := data["priceCurrency"]; ok {
				currency = fmt.Sprint(c)
			}
			return price, currency, true
		}
	}

	if offers, ok := data["offers"]; ok {
		if price, currency, ok := findPriceInJSONLD(offers); ok {
			return price, currency, true
		}
	}

	return 0, "", false
}

var pricePattern = regexp.MustCompile(`([0-9]+[\.,]?[0-9]*)`)

func parsePrice(raw string) (float64, error) {
	match := pricePattern.FindString(raw)
	if match == "" {
		return 0, fmt.Errorf("no price in %q", raw)
	}

	match = strings.ReplaceAll(match, ".", "")
	match = strings.ReplaceAll(match, ",", ".")

	var value float64
	_, err := fmt.Sscanf(match, "%f", &value)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func printResults(results []priceResult) {
	writer := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "URL\tPRICE\tDELTA\tSTATUS")
	for _, result := range results {
		if result.Err != nil {
			fmt.Fprintf(writer, "%s\tERROR\t-\t%v\n", result.URL, result.Err)
			continue
		}

		delta := "-"
		status := "same"
		if result.Delta != nil {
			delta = fmt.Sprintf("%+.2f", *result.Delta)
			if result.Decreased {
				status = "decrease"
			} else if *result.Delta > 0 {
				status = "increase"
			}
		}

		fmt.Fprintf(writer, "%s\t%.2f %s\t%s\t%s\n", result.URL, result.Price, result.Currency, delta, status)
	}
	writer.Flush()
}

func buildSummary(results []priceResult) string {
	var builder strings.Builder
	builder.WriteString("Daily price summary\n")
	for _, result := range results {
		if result.Err != nil {
			builder.WriteString(fmt.Sprintf("- %s: error (%v)\n", result.URL, result.Err))
			continue
		}
		status := "same"
		if result.Delta != nil {
			if result.Decreased {
				status = fmt.Sprintf("decrease %+.2f", *result.Delta)
			} else if *result.Delta > 0 {
				status = fmt.Sprintf("increase %+.2f", *result.Delta)
			}
		}
		builder.WriteString(fmt.Sprintf("- %s: %.2f %s (%s)\n", result.URL, result.Price, result.Currency, status))
	}
	return builder.String()
}

func sendTelegramSummary(summary string) error {
	token := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN"))
	chatID := strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID"))
	if token == "" || chatID == "" {
		return nil
	}

	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	payload := fmt.Sprintf("chat_id=%s&text=%s", chatID, urlEncode(summary))

	resp, err := http.Post(endpoint, "application/x-www-form-urlencoded", strings.NewReader(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram API error: %s", strings.TrimSpace(string(body)))
	}

	return nil
}

func urlEncode(input string) string {
	replacer := strings.NewReplacer(
		"%", "%25",
		"\n", "%0A",
		" ", "%20",
		"#", "%23",
		"&", "%26",
		"+", "%2B",
	)
	return replacer.Replace(input)
}
