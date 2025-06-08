package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"time"
)

// StatusResponse represents the structure of Kibana status API response
type StatusResponse struct {
	Status struct {
		Overall struct {
			State string `json:"state"`
		} `json:"overall"`
	} `json:"status"`
}

func main() {
	// Common flags
	url := flag.String("url", "http://kibana:5601", "Kibana base URL")
	timeout := flag.Int("timeout", 30, "Request timeout in seconds")
	insecure := flag.Bool("insecure", false, "Skip TLS certificate verification")
	caFile := flag.String("ca", "", "Path to custom CA certificate file")
	retries := flag.Int("retries", 3, "Number of retry attempts per check")
	retryDelay := flag.Int("retry-delay", 5, "Delay between retries in seconds")
	verbose := flag.Bool("verbose", false, "Enable verbose output")
	
	jsonFile := flag.String("json", "", "JSON file path for saved-objects operation")
	savedObjEndpoint := flag.String("endpoint", "/api/saved_objects/_import?overwrite=true", "Endpoint for saved-objects operation")
	
	// Status check flags
	maxWaitTime := flag.Int("max-wait", 300, "Maximum time to wait for green status in seconds")
	checkInterval := flag.Int("check-interval", 5, "Interval between status checks in seconds")
	
	flag.Parse()

	// Configure HTTP client
	transport := &http.Transport{}
	
	// Handle TLS configuration
	if *insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	} else if *caFile != "" {
		// Load custom CA cert
		caCert, err := os.ReadFile(*caFile)
		if err != nil {
			fmt.Printf("Error loading CA certificate: %v\n", err)
			os.Exit(1)
		}
		
		// Create a new cert pool and add the CA
		rootCAs, _ := x509.SystemCertPool()
		if rootCAs == nil {
			rootCAs = x509.NewCertPool()
		}
		
		// Append our CA cert to the system pool
		if ok := rootCAs.AppendCertsFromPEM(caCert); !ok {
			fmt.Println("Failed to append CA certificate")
			os.Exit(1)
		}
		
		// Trust the augmented cert pool in our client
		transport.TLSClientConfig = &tls.Config{
			RootCAs: rootCAs,
		}
	}
	
	client := &http.Client{
		Timeout:   time.Duration(*timeout) * time.Second,
		Transport: transport,
	}

        statusUrl := fmt.Sprintf("%s/api/status", *url)
        waitForGreenStatus(client, statusUrl, *maxWaitTime, *checkInterval, *retries, *retryDelay, *verbose)
        if *jsonFile == "" {
                fmt.Println("Error: --json flag required for savedobj operation")
                os.Exit(1)
        }
        savedObjUrl := fmt.Sprintf("%s%s", *url, *savedObjEndpoint)
        postSavedObjects(client, savedObjUrl, *jsonFile, *retries, *retryDelay, *verbose)
}

// Wait for Kibana status to turn green with a maximum wait time
func waitForGreenStatus(client *http.Client, url string, maxWaitTime int, checkInterval int, retries int, retryDelay int, verbose bool) {
	if verbose {
		fmt.Printf("Waiting for Kibana status to turn green (max %d seconds)\n", maxWaitTime)
	}
	
	startTime := time.Now()
	endTime := startTime.Add(time.Duration(maxWaitTime) * time.Second)
	
	for time.Now().Before(endTime) {
		status, err := getKibanaStatus(client, url, verbose)
		
		if err == nil {
			if status == "green" {
				fmt.Println("Kibana status is green")
				return
			} else if verbose {
				fmt.Printf("Current status: %s (waiting for green)\n", status)
			}
		} else if verbose {
			fmt.Printf("Error checking status: %v\n", err)
		}
		
		timeLeft := endTime.Sub(time.Now()).Seconds()
		if timeLeft <= 0 {
			break
		}
		
		if verbose {
			fmt.Printf("Waiting %d seconds before next check (%.0f seconds remaining)\n", 
				checkInterval, timeLeft)
		}
		
		time.Sleep(time.Duration(checkInterval) * time.Second)
	}
	
	fmt.Printf("Timed out after %d seconds waiting for Kibana to turn green\n", maxWaitTime)
	os.Exit(1)
}

func getKibanaStatus(client *http.Client, url string, verbose bool) (string, error) {
	if verbose {
		fmt.Printf("Checking Kibana status at %s\n", url)
	}
	
	// Make the request
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	
	if verbose {
		fmt.Printf("Response status code: %d\n", resp.StatusCode)
	}
	
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	
	// Read and parse the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}
	
	if verbose {
		fmt.Println("Response received successfully")
	}
	
	var statusResp StatusResponse
	if err := json.Unmarshal(body, &statusResp); err != nil {
		if verbose {
			fmt.Printf("Raw response: %s\n", string(body))
		}
		return "", fmt.Errorf("failed to parse JSON response: %w", err)
	}
	
	return statusResp.Status.Overall.State, nil
}

func postSavedObjects(client *http.Client, url string, jsonFile string, retries int, retryDelay int, verbose bool) {
	// Read the JSON file
	jsonData, err := os.ReadFile(jsonFile)
	if err != nil {
		fmt.Printf("Error reading JSON file: %v\n", err)
		os.Exit(1)
	}
	
	// Try to post saved objects with retries
	var lastErr error
	for attempt := 0; attempt < retries; attempt++ {
		if verbose && attempt > 0 {
			fmt.Printf("Retry attempt %d/%d\n", attempt+1, retries)
		}
		
		err := postMultipartToKibana(client, url, jsonData, verbose)
		if err == nil {
			fmt.Println("Successfully posted saved objects to Kibana")
			os.Exit(0)
		}
		
		lastErr = err
		if verbose {
			fmt.Printf("Error: %v\n", err)
		}
		
		if attempt < retries-1 {
			time.Sleep(time.Duration(retryDelay) * time.Second)
		}
	}
	
	fmt.Printf("Failed after %d attempts: %v\n", retries, lastErr)
	os.Exit(1)
}

func postMultipartToKibana(client *http.Client, url string, fileData []byte, verbose bool) error {
	if verbose {
		fmt.Println("Using multipart/form-data for import endpoint")
	}

	// Create a buffer to write our multipart form to
	var requestBody bytes.Buffer
	multipartWriter := multipart.NewWriter(&requestBody)

	// Create a form file field for the ndjson file
	fileWriter, err := multipartWriter.CreateFormFile("file", "import.ndjson")
	if err != nil {
		return fmt.Errorf("failed to create form file: %w", err)
	}

	// Copy the file data to the form
	if _, err := fileWriter.Write(fileData); err != nil {
		return fmt.Errorf("failed to write file data: %w", err)
	}

	// Close the multipart writer to finalize the form
	if err := multipartWriter.Close(); err != nil {
		return fmt.Errorf("failed to close multipart writer: %w", err)
	}

	// Create the request with the multipart form body
	req, err := http.NewRequest("POST", url, &requestBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set the content type header to the multipart form content type
	req.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	req.Header.Set("kbn-xsrf", "true")

	// Make the request
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if verbose {
		fmt.Printf("Response status code: %d\n", resp.StatusCode)
		fmt.Printf("Response body: %s\n", string(body))
	}

	// Check response status
	if resp.StatusCode >= 400 {
		return fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}
