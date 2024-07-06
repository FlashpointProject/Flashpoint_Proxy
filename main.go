package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FlashpointProject/zipfs"
	"github.com/elazarl/goproxy"
)

type ServerSettings struct {
	RootPath             string            `json:"rootPath"`
	GameDataPath         string            `json:"gameDataPath"`
	LegacyPHPPath        string            `json:"legacyPHPPath"`
	LegacyCGIBINPath     string            `json:"legacyCGIBINPath"`
	LegacyHTDOCSPath     string            `json:"legacyHTDOCSPath"`
	PhpCgiPath           string            `json:"phpCgiPath"`
	UseInfinityServer    bool              `json:"useInfinityServer"`
	InfinityServerURL    string            `json:"infinityServerURL"`
	HandleLegacyRequests bool              `json:"handleLegacyRequests"`
	ExternalLegacyPort   string            `json:"externalLegacyPort"`
	ProxyPort            string            `json:"proxyPort"`
	ServerHTTPPort       string            `json:"serverHTTPPort"`
	UseMad4FP            bool              `json:"useMad4FP"`
	EnableHttpsProxy     bool              `json:"enableHttpsProxy"`
	AllowCrossDomain     bool              `json:"allowCrossDomain"`
	VerboseLogging       bool              `json:"verboseLogging"`
	ApiPrefix            string            `json:"apiPrefix"`
	OverridePaths        []string          `json:"overridePaths"`
	LegacyOverridePaths  []string          `json:"legacyOverridePaths"`
	ExternalFilePaths    []string          `json:"externalFilePaths"`
	ExtScriptTypes       []string          `json:"extScriptTypes"`
	ExtIndexTypes        []string          `json:"extIndexTypes"`
	ExtGzippeddTypes     []string          `json:"extGzippedTypes"`
	ExtMimeTypes         map[string]string `json:"extMimeTypes"`
}

// ExtApplicationTypes is a map that holds the content types of different file extensions
var serverSettings ServerSettings
var proxy *goproxy.ProxyHttpServer
var cwd string

func initServer() {
	// Get the CWD of this application
	exe, err := os.Executable()
	if err != nil {
		panic(err)
	}
	cwd = filepath.Dir(exe)

	// Load the content types from the JSON file
	data, err := os.ReadFile(filepath.Join(cwd, "proxySettings.json"))
	if err != nil {
		panic(err)
	}

	// Unmarshal the JSON data into a Config struct
	err = json.Unmarshal(data, &serverSettings)
	if err != nil {
		panic(err)
	}

	// Get all of the parameters passed in
	// TODO: Figure out a way to (partially?) automate everything that's going on below
	// TODO: Improve descriptions
	rootPath := flag.String("rootPath", serverSettings.RootPath, "The path that other relative paths use as a base")
	gameDataPath := flag.String("gameRootPath", serverSettings.GameDataPath, "This is the path where to find the zips")
	legacyPHPPath := flag.String("legacyPHPPath", serverSettings.LegacyPHPPath, "This is the path for PHP")
	legacyCGIBINPath := flag.String("legacyCGIBINPath", serverSettings.LegacyCGIBINPath, "This is the path for CGI-BIN")
	legacyHTDOCSPath := flag.String("legacyHTDOCSPath", serverSettings.LegacyHTDOCSPath, "This is the path for HTDOCS")
	phpCgiPath := flag.String("phpCgiPath", serverSettings.PhpCgiPath, "Path to PHP CGI executable")
	useInfinityServer := flag.Bool("useInfinityServer", serverSettings.UseInfinityServer, "Whether to use the infinity server or not")
	infinityServerURL := flag.String("infinityServerURL", serverSettings.InfinityServerURL, "The URL of the infinity server")
	handleLegacyRequests := flag.Bool("handleLegacyRequests", serverSettings.HandleLegacyRequests, "Whether to handle legacy requests internally (true) or externally (false)")
	externalLegacyPort := flag.String("externalLegacyPort", serverSettings.ExternalLegacyPort, "The port that the external legacy server is running on (if handling legacy is disabled).")
	proxyPort := flag.String("proxyPort", serverSettings.ProxyPort, "proxy listen port")
	serverHttpPort := flag.String("serverHttpPort", serverSettings.ServerHTTPPort, "zip server http listen port")
	useMad4FP := flag.Bool("UseMad4FP", serverSettings.UseMad4FP, "flag to turn on/off Mad4FP.")
	enableHttpsProxy := flag.Bool("enableHttpsProxy", serverSettings.EnableHttpsProxy, "Whether to enable HTTPS proxying or not")
	allowCrossDomain := flag.Bool("allowCrossDomain", serverSettings.AllowCrossDomain, "Whether to allow cross-domain requests")
	verboseLogging := flag.Bool("verboseLogging", serverSettings.VerboseLogging, "should every proxy request be logged to stdout")
	apiPrefix := flag.String("apiPrefix", serverSettings.ApiPrefix, "apiPrefix is used to prefix any API call.")

	flag.Parse()

	// Apply all of the flags to the settings
	serverSettings.RootPath, err = filepath.Abs(strings.Trim(*rootPath, "\""))
	if err != nil {
		fmt.Println("Failed to get absolute root path")
		panic(err)
	}

	getAbsConfigPath := func(p string) string {
		if filepath.IsAbs(p) {
			return filepath.Clean(p)
		} else {
			return filepath.Join(serverSettings.RootPath, strings.Trim(p, "\"")) // Join also calls Clean
		}
	}

	serverSettings.GameDataPath = getAbsConfigPath(*gameDataPath)
	serverSettings.LegacyPHPPath = getAbsConfigPath(*legacyPHPPath)
	serverSettings.LegacyCGIBINPath = getAbsConfigPath(*legacyCGIBINPath)
	serverSettings.LegacyHTDOCSPath = getAbsConfigPath(*legacyHTDOCSPath)
	serverSettings.PhpCgiPath = getAbsConfigPath(*phpCgiPath)
	serverSettings.UseInfinityServer = *useInfinityServer
	serverSettings.InfinityServerURL = *infinityServerURL
	serverSettings.HandleLegacyRequests = *handleLegacyRequests
	serverSettings.ExternalLegacyPort = *externalLegacyPort
	serverSettings.ProxyPort = *proxyPort
	serverSettings.ServerHTTPPort = *serverHttpPort
	serverSettings.UseMad4FP = *useMad4FP
	serverSettings.EnableHttpsProxy = *enableHttpsProxy
	serverSettings.AllowCrossDomain = *allowCrossDomain
	serverSettings.VerboseLogging = *verboseLogging
	serverSettings.ApiPrefix = *apiPrefix

	// Print out all path settings
	fmt.Println("Root Path:", serverSettings.RootPath)
	fmt.Println("Game Data Path:", serverSettings.GameDataPath)
	fmt.Println("Legacy PHP Path:", serverSettings.LegacyPHPPath)
	fmt.Println("Legacy CGI-BIN Path:", serverSettings.LegacyCGIBINPath)
	fmt.Println("Legacy HTDOCS Path:", serverSettings.LegacyHTDOCSPath)
	fmt.Println("PHP-CGI Path:", serverSettings.PhpCgiPath)

	// Setup the proxy
	proxy = goproxy.NewProxyHttpServer()
	proxy.Verbose = serverSettings.VerboseLogging
	fmt.Println("Proxy Server started on port", serverSettings.ProxyPort)
	fmt.Println("Zip Server started on port", serverSettings.ServerHTTPPort)
}

func setContentType(r *http.Request, resp *http.Response) {
	if r == nil || resp == nil {
		return
	}

	resp.Header.Del("Content-Type")

	rext := strings.ToLower(filepath.Ext(resp.Header.Get("ZIPSVR_FILENAME")))
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(r.URL.Path)))
	mime := ""

	// If the request already has an extension, fetch the mime via extension
	if ext != "" {
		resp.Header.Set("Content-Type", serverSettings.ExtMimeTypes[ext[1:]])
		mime = serverSettings.ExtMimeTypes[ext[1:]]
		if mime != "" && len(ext) > 1 {
			resp.Header.Set("Content-Type", mime)
			e := ext[1:]
			// If pre-compressed set encoding type
			for _, element := range serverSettings.ExtGzippeddTypes {
				if element == e {
					resp.Header.Set("Content-Encoding", "gzip")
					break // String found, no need to continue iterating
				}
			}
		}
	}

	// If the response has an extension, try and fetch the mime for that via extension
	if mime == "" && rext != "" {
		resp.Header.Set("Content-Type", serverSettings.ExtMimeTypes[rext[1:]])
		mime = serverSettings.ExtMimeTypes[rext[1:]]
		if mime != "" && len(rext) > 1 {
			resp.Header.Set("Content-Type", mime)
			e := rext[1:]
			// If pre-compressed set encoding type
			for _, element := range serverSettings.ExtGzippeddTypes {
				if element == e {
					resp.Header.Set("Content-Encoding", "gzip")
					break // String found, no need to continue iterating
				}
			}
		}
	}

	// Set content type header
	if mime != "" {
		resp.Header.Set("Content-Type", mime)
	}
}

func handleRequest(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	// Remove port from host if exists (old apps don't clean it before sending requests?)
	r.URL.Host = strings.Split(r.URL.Host, ":")[0]
	// Clone the body into both requests by reading and making 2 new readers
	contents, _ := io.ReadAll(r.Body)

	// Copy the original request
	gamezipRequest := &http.Request{
		Method: r.Method,
		URL: &url.URL{
			Scheme:   "http",
			Host:     "127.0.0.1:" + serverSettings.ServerHTTPPort,
			Path:     "content/" + r.URL.Host + r.URL.Path,
			RawQuery: r.URL.RawQuery,
		},
		Header: r.Header,
		Body:   nil,
	}
	gamezipRequest.Body = io.NopCloser(bytes.NewReader(contents))

	// Make the request to the zip server.
	client := &http.Client{}
	proxyReq, err := http.NewRequest(gamezipRequest.Method, gamezipRequest.URL.String(), gamezipRequest.Body)
	if err != nil {
		fmt.Printf("UNHANDLED GAMEZIP ERROR: %s\n", err)
	}
	proxyReq.Header = gamezipRequest.Header

	proxyResp, err := client.Do(proxyReq)
	if err != nil {
		fmt.Printf("UNHANDLED GAMEZIP SERVER ERROR: %s\n", err)
	}
	if proxyResp.StatusCode >= 500 {
		fmt.Println("Gamezip Server Error: ", proxyResp.StatusCode)
	}

	// Check Legacy
	if proxyResp.StatusCode >= 400 {
		// Copy the original request
		legacyRequest := &http.Request{
			Method: r.Method,
			URL: &url.URL{
				Scheme:   "http",
				Host:     r.URL.Host,
				Path:     r.URL.Path,
				RawQuery: r.URL.RawQuery,
			},
			Header: r.Header,
			Body:   nil,
		}
		// Copy in a new body reader
		legacyRequest.Body = io.NopCloser(bytes.NewReader(contents))

		// Choose which legacy method we're using
		if serverSettings.HandleLegacyRequests {
			// If internal, skip actual networking
			resRecorder := httptest.NewRecorder()
			ServeLegacy(resRecorder, legacyRequest)
			proxyResp = resRecorder.Result()
		} else {
			// Set the Proxy URL and apply it to the Transpor layer so that the request respects the proxy.
			proxyURL, _ := url.Parse("http://127.0.0.1:" + serverSettings.ExternalLegacyPort)
			proxy := http.ProxyURL(proxyURL)
			transport := &http.Transport{Proxy: proxy}

			// A custom Dialer is required for the "localflash" urls, instead of using the DNS, we use this.
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				//Set Dialer timeout and keepalive to 30 seconds and force the address to localhost.
				dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
				addr = "127.0.0.1:" + serverSettings.ExternalLegacyPort
				return dialer.DialContext(ctx, network, addr)
			}

			// Make the request with the custom transport
			client := &http.Client{Transport: transport, Timeout: 300 * time.Second}
			legacyResp, err := client.Do(legacyRequest)
			// An error occured, log it for debug purposes
			if err == nil {
				fmt.Printf("\tServing from External Legacy...\n")
				proxyResp = legacyResp
			} else {
				fmt.Printf("UNHANDLED EXTERNAL LEGACY ERROR: %s\n", err)
			}
		}
	}

	// Update the content type based upon ext for now.
	setContentType(r, proxyResp)

	// Add extra headers
	proxyResp.Header.Set("Access-Control-Allow-Headers", "*")
	proxyResp.Header.Set("Access-Control-Allow-Methods", "*")
	proxyResp.Header.Set("Access-Control-Allow-Origin", "*")
	// Keep Alive
	if strings.ToLower(r.Header.Get("Connection")) == "keep-alive" {
		proxyResp.Header.Set("Connection", "Keep-Alive")
		proxyResp.Header.Set("Keep-Alive", "timeout=5; max=100")
	}

	return r, proxyResp
}

func main() {
	initServer()
	// To create CA cert, refer to https://wiki.mozilla.org/SecurityEngineering/x509Certs#Self_Signed_Certs
	// Replace CA in GoProxy
	certData := []byte(`-----BEGIN CERTIFICATE-----
MIICJDCCAcsCFFCWJV/hBHpY18k/14yUbDA6V/TTMAoGCCqGSM49BAMCMIGTMQsw
CQYDVQQGEwJVUzETMBEGA1UECAwKU29tZS1TdGF0ZTEoMCYGA1UECgwfRmxhc2hw
b2ludCBQcm94eSBVbnRydXN0ZWQgTUlUTTEoMCYGA1UECwwfRmxhc2hwb2ludCBQ
cm94eSBVbnRydXN0ZWQgTUlUTTEbMBkGA1UEAwwSZnBwcm94eS5sb2NhbC5zaXRl
MCAXDTIzMTAxNDEzNTQxNVoYDzIxMjMwOTIwMTM1NDE1WjCBkzELMAkGA1UEBhMC
VVMxEzARBgNVBAgMClNvbWUtU3RhdGUxKDAmBgNVBAoMH0ZsYXNocG9pbnQgUHJv
eHkgVW50cnVzdGVkIE1JVE0xKDAmBgNVBAsMH0ZsYXNocG9pbnQgUHJveHkgVW50
cnVzdGVkIE1JVE0xGzAZBgNVBAMMEmZwcHJveHkubG9jYWwuc2l0ZTBZMBMGByqG
SM49AgEGCCqGSM49AwEHA0IABDOkMb4Fb+waYfEXg5OszAyjNqcp8PLTqSC2fcfC
gX3Wqgvq4Vf46F4FViDKyo+E+6fOm3MauI3Vg2FGKUXf9jowCgYIKoZIzj0EAwID
RwAwRAIgHyjrkkCwuOQm5JO5SKeH3Om8dQm6m6a+1k5max2RqakCICQRzrm0ERo4
siAXSthMrOdDignP/cM10AcBe/J00Vw8
-----END CERTIFICATE-----`)
	keyData := []byte(`-----BEGIN EC PARAMETERS-----
BggqhkjOPQMBBw==
-----END EC PARAMETERS-----
-----BEGIN EC PRIVATE KEY-----
MHcCAQEEIGfj1mtowe1WiAMA3mK1VjgXV1lgUkliUxnk6lr5y/g5oAoGCCqGSM49
AwEHoUQDQgAEM6QxvgVv7Bph8ReDk6zMDKM2pynw8tOpILZ9x8KBfdaqC+rhV/jo
XgVWIMrKj4T7p86bcxq4jdWDYUYpRd/2Og==
-----END EC PRIVATE KEY-----`)

	cert, err := tls.X509KeyPair(certData, keyData)
	if err != nil {
		panic(err)
	}

	goproxy.MitmConnect.TLSConfig = goproxy.TLSConfigFromCA(&cert)

	// Handle HTTPS requests (DOES NOT HANDLE HTTP)
	if serverSettings.EnableHttpsProxy {
		proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	} else {
		proxy.OnRequest().HandleConnect(goproxy.AlwaysReject)
	}
	proxy.OnRequest().HijackConnect(func(req *http.Request, client net.Conn, ctx *goproxy.ProxyCtx) {
		_, resp := handleRequest(req, ctx)
		err := resp.Write(client)
		if err != nil {
			fmt.Printf("Error writing response to client: %s\n", err)
		}
		client.Close()
	})

	// Handle HTTP requests (DOES NOT HANDLE HTTPS)
	proxy.OnRequest().DoFunc(func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		return handleRequest(r, ctx)
	})

	// Start ZIP server
	go func() {
		//TODO: Update these to be modifiable in the properties json.
		//TODO: Also update the "fpProxy/api/" to be in the properties json.
		log.Fatal(http.ListenAndServe("127.0.0.1:"+serverSettings.ServerHTTPPort,
			zipfs.EmptyFileServer(
				serverSettings.ApiPrefix,
				"",
				serverSettings.VerboseLogging,
				serverSettings.ExtIndexTypes,
				serverSettings.GameDataPath,
				serverSettings.PhpCgiPath,
				serverSettings.ExtMimeTypes,
				serverSettings.OverridePaths,
				serverSettings.LegacyHTDOCSPath,
			),
		))
	}()

	// Start proxy server
	log.Fatal(http.ListenAndServe("127.0.0.1:"+serverSettings.ProxyPort, http.AllowQuerySemicolons(proxy)))
}
