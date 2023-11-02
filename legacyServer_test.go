package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"testing"
)

type legacyServerTestResponse struct {
	statusCode int
	headers    *http.Header
	body       []byte
	savedFile  *string
}

type legacyServerTest struct {
	request  *http.Request
	response *legacyServerTestResponse
}

var testServerSettingsBuilt = false
var testServerSettings = ServerSettings{
	UseMad4FP:         false,
	UseInfinityServer: false,
	InfinityServerURL: "https://infinity.unstable.life/Flashpoint/Legacy/htdocs/",
	LegacyHTDOCSPath:  "",
	ExtScriptTypes: []string{
		"php",
	},
	ExtIndexTypes: []string{
		"htm", "html", "php",
	},
	PhpCgiPath: `J:\Data\Flashpoint\Legacy\php-cgi.exe`,
}

func setup(settings *ServerSettings) {
	if !testServerSettingsBuilt {
		testServerSettingsBuilt = true
		cwd, err := os.Getwd()
		if err != nil {
			panic(err)
		}
		testServerSettings.LegacyHTDOCSPath = path.Join(cwd, "testdata", "htdocs")
		settings.LegacyHTDOCSPath = testServerSettings.LegacyHTDOCSPath
	}
	// Cleanup and remake htdocs
	err := os.RemoveAll(testServerSettings.LegacyHTDOCSPath)
	if err != nil {
		panic(err)
	}
	err = os.MkdirAll(testServerSettings.LegacyHTDOCSPath, os.ModePerm)
	if err != nil {
		panic(err)
	}
	serverSettings = *settings
}

func (test *legacyServerTest) run() error {
	writer := httptest.NewRecorder()
	ServeLegacy(writer, test.request)
	res := writer.Result()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("failed to read body %s", err)
	}
	defer res.Body.Close()
	if len(test.response.body) != 0 {
		// Make sure body matches
		if !bytes.Equal(body, test.response.body) {
			return fmt.Errorf("expected body %s, got %s", test.response.body, body)
		}
	}
	if test.response.statusCode != -1 && res.StatusCode != test.response.statusCode {
		// Make sure status code matches
		return fmt.Errorf("expected status code %d, got %d", test.response.statusCode, res.StatusCode)
	}
	if test.response.headers != nil {
		lowerCaseHeader := make(http.Header)
		for key, values := range res.Header {
			// Convert the key to lowercase
			lowerKey := strings.ToLower(key)
			// Copy the values to the new header
			lowerCaseHeader[lowerKey] = values
		}
		// Make sure all headers are present
		for key, values := range *test.response.headers {
			lowerKey := strings.ToLower(key)
			if lowerKey == "content-length" {
				val, err := strconv.Atoi(values[0])
				if err != nil {
					return fmt.Errorf("invalid test content length %s: %s", values[0], err)
				}
				if res.ContentLength == -1 {
					// Unknown length, must read body?
					if len(body) != val {
						return fmt.Errorf("expected content length %d, got %d", val, len(body))
					}
				} else {
					if res.ContentLength != int64(val) {
						return fmt.Errorf("expected content length %d, got %d", val, res.ContentLength)
					}
				}
			} else {
				if lowerCaseHeader[lowerKey] == nil || lowerCaseHeader[key][0] != values[0] {
					badValue := "<nil>"
					if lowerCaseHeader[lowerKey] != nil {
						badValue = lowerCaseHeader[lowerKey][0]
					}
					return fmt.Errorf("expected header %s to be %s, got %s", key, values[0], badValue)
				}
			}
		}
	}
	if test.response.savedFile != nil {
		// Check the file exists
		_, err := os.Stat(*test.response.savedFile)
		if err != nil {
			return fmt.Errorf("expected file %s to exist, got error %s", *test.response.savedFile, err)
		}
	}
	return nil
}

func TestServeLegacy404(t *testing.T) {
	setup(&testServerSettings)

	test := &legacyServerTest{
		request: makeNewRequest("GET", "https://example.com/404-example"),
		response: &legacyServerTestResponse{
			statusCode: http.StatusNotFound,
		},
	}

	err := test.run()
	if err != nil {
		t.Error(err)
	}
}

func TestServeLegacy200(t *testing.T) {
	setup(&testServerSettings)

	// Write a test file
	testData := []byte("success")
	testFile := path.Join(testServerSettings.LegacyHTDOCSPath, "example.com", "test.txt")
	// Make directory path
	err := os.MkdirAll(path.Dir(testFile), os.ModePerm)
	if err != nil {
		t.Error(err)
	}
	err = os.WriteFile(testFile, testData, os.ModePerm)
	if err != nil {
		t.Error(err)
	}

	headers := &http.Header{}
	headers.Set("Content-Length", strconv.Itoa(len(testData)))
	test := &legacyServerTest{
		request: makeNewRequest("GET", "http://example.com/test.txt"),
		response: &legacyServerTestResponse{
			statusCode: http.StatusOK,
			headers:    headers,
		},
	}

	err = test.run()
	if err != nil {
		t.Error(err)
	}
}

func TestServeLegacy200WithQuery(t *testing.T) {
	setup(&testServerSettings)

	// Write 2 test files, to validate that the query is being used
	testData := []byte("success")
	testDataQuery := []byte("success query")
	testFile := path.Join(testServerSettings.LegacyHTDOCSPath, "example.com", "test.txt")
	// Assume all filenames are url encoded
	testFileQuery := path.Join(testServerSettings.LegacyHTDOCSPath, "example.com", url.PathEscape("test.txt?query=true"))

	// Make directory path
	err := os.MkdirAll(path.Dir(testFile), os.ModePerm)
	if err != nil {
		t.Error(err)
	}
	err = os.WriteFile(testFile, testData, os.ModePerm)
	if err != nil {
		t.Error(err)
	}
	err = os.WriteFile(testFileQuery, testDataQuery, os.ModePerm)
	if err != nil {
		t.Error(err)
	}

	headers := &http.Header{}
	headers.Set("Content-Length", strconv.Itoa(len(testDataQuery)))
	test := &legacyServerTest{
		request: makeNewRequest("GET", "http://example.com/test.txt?query=true"),
		response: &legacyServerTestResponse{
			statusCode: http.StatusOK,
			headers:    headers,
		},
	}

	err = test.run()
	if err != nil {
		t.Error(err)
	}
}

func TestServeLegacy200Index(t *testing.T) {
	setup(&testServerSettings)

	// Write a test file
	testData := []byte("success")
	testFile := path.Join(testServerSettings.LegacyHTDOCSPath, "example.com", "index.htm")
	// Make directory path
	err := os.MkdirAll(path.Dir(testFile), os.ModePerm)
	if err != nil {
		t.Error(err)
	}
	err = os.WriteFile(testFile, testData, os.ModePerm)
	if err != nil {
		t.Error(err)
	}

	headers := &http.Header{}
	headers.Set("Content-Length", strconv.Itoa(len(testData)))
	test := &legacyServerTest{
		request: makeNewRequest("GET", "http://example.com/"),
		response: &legacyServerTestResponse{
			statusCode: http.StatusOK,
			headers:    headers,
		},
	}

	err = test.run()
	if err != nil {
		t.Error(err)
	}
}

func TestServeLegacy200Script(t *testing.T) {
	setup(&testServerSettings)

	// Write a test file
	testStr := "success"
	testData := []byte(fmt.Sprintf("<?php echo \"%s\"; ?>", testStr))
	testFile := path.Join(testServerSettings.LegacyHTDOCSPath, "example.com", "index.php")
	// Make directory path
	err := os.MkdirAll(path.Dir(testFile), os.ModePerm)
	if err != nil {
		t.Error(err)
	}
	err = os.WriteFile(testFile, testData, os.ModePerm)
	if err != nil {
		t.Error(err)
	}

	headers := &http.Header{}
	headers.Set("Content-Length", strconv.Itoa(len(testStr)))
	test := &legacyServerTest{
		request: makeNewRequest("GET", "http://example.com/index.php"),
		response: &legacyServerTestResponse{
			statusCode: http.StatusOK,
			headers:    headers,
		},
	}

	err = test.run()
	if err != nil {
		t.Error(err)
	}
}

func TestServeLegacyDisabledOnline(t *testing.T) {
	setup(&testServerSettings)

	test := &legacyServerTest{
		request: makeNewRequest("GET", "http://andkon.com/grey/grey.swf"),
		response: &legacyServerTestResponse{
			statusCode: http.StatusNotFound,
		},
	}

	err := test.run()
	if err != nil {
		t.Error(err)
	}
}

func TestServeLegacyOnline200(t *testing.T) {
	settings := testServerSettings
	settings.UseInfinityServer = true
	setup(&settings)

	savedFile := path.Join(settings.LegacyHTDOCSPath, "andkon.com", "grey", "grey.swf")
	headers := &http.Header{}
	headers.Set("Content-Length", "1065015")
	test := &legacyServerTest{
		request: makeNewRequest("GET", "http://andkon.com/grey/grey.swf"),
		response: &legacyServerTestResponse{
			statusCode: http.StatusOK,
			savedFile:  &savedFile,
			headers:    headers,
		},
	}

	err := test.run()
	if err != nil {
		t.Error(err)
	}
}

func TestServeLegacyOnline200Index(t *testing.T) {
	settings := testServerSettings
	settings.UseInfinityServer = true
	setup(&settings)

	savedFile := path.Join(settings.LegacyHTDOCSPath, "kongregate.com", "ChuckTheSheep", "index.htm")
	test := &legacyServerTest{
		request: makeNewRequest("GET", "http://kongregate.com/ChuckTheSheep/"),
		response: &legacyServerTestResponse{
			statusCode: http.StatusOK,
			savedFile:  &savedFile,
		},
	}

	err := test.run()
	if err != nil {
		t.Error(err)
	}
}

func TestServeLegacyDisabledMad4fp(t *testing.T) {
	setup(&testServerSettings)

	test := &legacyServerTest{
		request: makeNewRequest("GET", "http://flashpointarchive.org/images/logo.svg"),
		response: &legacyServerTestResponse{
			statusCode: http.StatusNotFound,
		},
	}

	err := test.run()
	if err != nil {
		t.Error(err)
	}
}

func TestServeLegacyMad4fp200(t *testing.T) {
	settings := testServerSettings
	settings.UseMad4FP = true
	setup(&settings)

	savedFile := path.Join(settings.LegacyHTDOCSPath, "content", "flashpointarchive.org", "images", "logo.svg")
	test := &legacyServerTest{
		request: makeNewRequest("GET", "http://flashpointarchive.org/images/logo.svg"),
		response: &legacyServerTestResponse{
			statusCode: http.StatusOK,
			savedFile:  &savedFile,
		},
	}

	err := test.run()
	if err != nil {
		t.Error(err)
	}
}

func makeNewRequest(method string, url string) *http.Request {
	request, err := http.NewRequest(method, url, nil)
	if err != nil {
		panic(err)
	}

	return request
}
