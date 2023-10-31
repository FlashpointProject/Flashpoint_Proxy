package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/krum110487/zipfs"
)

// Tries to serve a legacy file if available
func ServeLegacy(w http.ResponseWriter, r *http.Request) {

	// @TODO PERFORM REQUEST MODIFICATION HERE

	relPath := filepath.ToSlash(path.Join(r.URL.Host, r.URL.Path))
	relPathWithQuery := filepath.ToSlash(path.Join(r.URL.Host, r.URL.Path+url.PathEscape("?"+r.URL.RawQuery)))
	hasQuery := r.URL.RawQuery != ""

	// Returns the data from the matching method, finishing the response
	successCloser := func(reader io.ReadCloser, fileName string, lastModified string) {
		w.Header().Set("Last-Modified", lastModified)
		w.Header().Set("ZIPSVR_FILENAME", fileName)
		size, err := io.Copy(w, reader)
		if err != nil {
			fmt.Printf("Error copying file to response: %s\n", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		} else {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
			w.WriteHeader(http.StatusOK)
		}
	}

	/** Overview
	 * Create groups of paths:
	 * 1. Exact Files
	 * 2. Directory Index Files
	 *
	 * Peform each check in order, checking each group in order within, to find a match:
	 * 1. Local File
	 * 2. Online Server
	 * 3. Special Behaviour (MAD4FP)
	 */

	// Building groups of paths

	// Local = All Paths
	// Online = All non-override paths
	// Special = Exact content path only (with index consideration)

	// Override paths

	exactContentPath := path.Join(serverSettings.LegacyHTDOCSPath, "content", relPath)
	exactFilePaths := []string{}
	exactOverrideFilePaths := []string{}
	indexFilePaths := []string{}
	indexOverrideFilePaths := []string{}

	// 1. Exact Files
	if hasQuery {
		for _, override := range serverSettings.LegacyOverridePaths {
			exactOverrideFilePaths = append(exactOverrideFilePaths, path.Join(serverSettings.LegacyHTDOCSPath, override, relPathWithQuery))
		}
		exactFilePaths = append(exactFilePaths, path.Join(serverSettings.LegacyHTDOCSPath, relPathWithQuery))
	}
	for _, override := range serverSettings.LegacyOverridePaths {
		exactOverrideFilePaths = append(exactOverrideFilePaths, path.Join(serverSettings.LegacyHTDOCSPath, override, relPath))
	}
	exactFilePaths = append(exactFilePaths, path.Join(serverSettings.LegacyHTDOCSPath, relPath))

	// 2. Directory Index Files
	for _, ext := range serverSettings.ExtIndexTypes {
		for _, override := range serverSettings.LegacyOverridePaths {
			indexOverrideFilePaths = append(indexOverrideFilePaths, path.Join(serverSettings.LegacyHTDOCSPath, override, relPath, "index."+ext))
		}
		indexFilePaths = append(indexFilePaths, path.Join(serverSettings.LegacyHTDOCSPath, relPath, "index."+ext))
	}

	// Try and find a valid file to respond with

	// 1. Local File
	for _, filePath := range append(append(append(exactFilePaths, exactOverrideFilePaths...), indexFilePaths...), indexOverrideFilePaths...) {
		// Check if file exists
		stats, err := os.Stat(filePath)
		if err == nil && !stats.IsDir() {
			// If it's a PHP file, let CGI handle instead
			for _, ext := range serverSettings.ExtScriptTypes {
				if filepath.Ext(filePath) == "."+ext {
					zipfs.Cgi(w, r, serverSettings.PhpCgiPath, filePath)
					return
				}
			}
			// File exists and is static, serve
			f, err := os.Open(filePath)
			if err != nil {
				// File exists but failed to open, server error
				fmt.Printf("[Legacy] Error reading file '%s': %s\n", filePath, err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer f.Close()
			fmt.Printf("[Legacy] Serving exact file: %s\n", filepath.ToSlash(filePath))
			successCloser(io.NopCloser(f), filePath, stats.ModTime().Format(time.RFC1123))
			return
		}
	}

	client := &http.Client{
		Timeout: time.Second * 10,
	}

	// 2. Online Server
	if serverSettings.UseInfinityServer {
		serverUrl := strings.TrimRight(serverSettings.InfinityServerURL, "/")
		for _, filePath := range append(exactFilePaths, indexFilePaths...) {
			// Create a new request to the online server
			relPath, err := filepath.Rel(serverSettings.LegacyHTDOCSPath, filePath)
			if err != nil {
				fmt.Printf("[Legacy] Error getting relative path for Infinity request: %s\n", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			url := serverUrl + "/" + strings.ReplaceAll(relPath, string([]rune{'\\'}), "/")
			liveReq, err := http.NewRequest("GET", url, nil)
			if err != nil {
				fmt.Printf("[Legacy] Error creating Infinity request: %s\n", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			liveReq.Header.Set("User-Agent", "Flashpoint Game Server")
			// Perform request
			resp, err := DoWebRequest(liveReq, client, 0)
			// If 200, serve and save
			if err == nil {
				serveLiveResponse(w, resp, filePath, successCloser, "Infinity")
				return
			}
		}
	}

	// 3. Special Behaviour (MAD4FP)
	if serverSettings.UseMad4FP {
		// Clone the entire request, to keep headers intact for better scraping
		liveReq := r.Clone(r.Context())
		liveReq.Header.Set("User-Agent", "Flashpoint Game Server MAD4FP")
		// Perform request
		resp, err := DoWebRequest(liveReq, client, 0)
		// If 200, serve and save
		if err == nil {
			serveLiveResponse(w, resp, exactContentPath, successCloser, "MAD4FP")
			return
		}
	}

	// No response from any method, assume not found
	http.NotFound(w, r)
}

// Serves a response from a live server, and saves the response body to the originally requested file
func serveLiveResponse(w http.ResponseWriter, resp *http.Response, filePath string, successCloser func(io.ReadCloser, string, string), sourceName string) {
	defer resp.Body.Close()
	lastModified := resp.Header.Get("Last-Modified")
	// Duplicate response body
	contents, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("[Legacy] Error reading %s response body: %s\n", sourceName, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Save file
	err = os.MkdirAll(filepath.Dir(filePath), os.ModePerm)
	if err != nil {
		fmt.Printf("[Legacy] Error saving %s response, cannot make directory: %s\n", sourceName, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	file, err := os.Create(filePath)
	if err != nil {
		fmt.Printf("[Legacy] Error saving %s response, cannot create file: %s\n", sourceName, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer file.Close()
	_, err = io.Copy(file, bytes.NewReader(contents))
	if err != nil {
		fmt.Printf("[Legacy] Error saving %s response, cannot write file: %s\n", sourceName, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if resp.Header.Get("Last-Modified") != "" {
		// Convert header to real time
		modifiedTime, err := time.Parse(time.RFC1123, lastModified)
		if err == nil {
			// Date converted successfuly, set time on file and return with response
			err = os.Chtimes(filePath, time.Now(), modifiedTime)
			if err != nil {
				fmt.Printf("[Legacy] Error saving %s response, cannot set modified time: %s\n", sourceName, err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			fmt.Printf("[Legacy] Serving %s file: %s\n", sourceName, filepath.ToSlash(filePath))
			successCloser(io.NopCloser(bytes.NewReader(contents)), filePath, lastModified)
			return
		}
	}
	// No last modified found, just use current time
	successCloser(io.NopCloser(bytes.NewReader(contents)), filePath, time.Now().Format(time.RFC1123))
}

// Treats non-200 responses as errors, and handles 429 responses with a retry timer
func DoWebRequest(r *http.Request, client *http.Client, depth int) (*http.Response, error) {
	resp, err := client.Do(r)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 429 {
		if depth > 3 {
			return resp, fmt.Errorf("Too many 429 responses")
		}
		// Rate limited, wait and try again
		timeToSleep := resp.Header.Get("Retry-After")
		timeToSleepInt, err := strconv.Atoi(timeToSleep)
		if err != nil {
			timeToSleepInt = 2
		}
		time.Sleep(time.Duration(timeToSleepInt) * time.Second)
		return DoWebRequest(r, client, depth+1)
	}
	if resp.StatusCode == 200 {
		return resp, nil
	}
	return resp, fmt.Errorf(resp.Status)
}
