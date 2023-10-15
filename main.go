package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elazarl/goproxy"
	"github.com/krum110487/zipfs"
)

type ProxySettings struct {
	AllowCrossDomain   bool              `json:"allowCrossDomain"`
	VerboseLogging     bool              `json:"verboseLogging"`
	ProxyPort          string            `json:"proxyPort"`
	ServerHTTPPort     string            `json:"serverHTTPPort"`
	ServerHTTPSPort    string            `json:"serverHTTPSPort"`
	GameRootPath       string            `json:"gameRootPath"`
	ApiPrefix          string            `json:"apiPrefix"`
	ExternalFilePaths  []string          `json:"externalFilePaths"`
	ExtScriptTypes     []string          `json:"extScriptTypes"`
	ExtIndexTypes      []string          `json:"extIndexTypes"`
	ExtMimeTypes       map[string]string `json:"extMimeTypes"`
	UseMad4FP          bool              `json:"useMad4FP"`
	LegacyGoPort       string            `json:"legacyGoPort"`
	LegacyPHPPort      string            `json:"legacyPHPPort"`
	LegacyPHPPath      string            `json:"legacyPHPPath"`
	LegacyUsePHPServer bool              `json:"legacyUsePHPServer"`
	LegacyHTDOCSPath   string            `json:"legacyHTDOCSPath"`
	LegacyCGIBINPath   string            `json:"legacyCGIBINPath"`
	PhpCgiPath         string            `json:"phpCgiPath"`
	OverridePaths      []string          `json:"overridePaths"`
}

// ExtApplicationTypes is a map that holds the content types of different file extensions
var proxySettings ProxySettings
var proxy *goproxy.ProxyHttpServer
var cwd string

func init() {
	// Load the content types from the JSON file
	data, err := os.ReadFile("proxySettings.json")
	if err != nil {
		panic(err)
	}

	// Unmarshal the JSON data into a Config struct
	err = json.Unmarshal(data, &proxySettings)
	if err != nil {
		panic(err)
	}

	//Get the CWD of this application
	exe, err := os.Executable()
	if err != nil {
		panic(err)
	}
	cwd = strings.ReplaceAll(filepath.Dir(exe), "\\", "/")

	//TODO: Update proxySettings.LegacyHTDOCSPath AND proxySettings.LegacyPHPPath for the default values!

	//Get all of the paramaters passed in.
	verboseLogging := flag.Bool("v", false, "should every proxy request be logged to stdout")
	proxyPort := flag.Int("proxyPort", 22500, "proxy listen port")
	serverHTTPPort := flag.Int("serverHttpPort", 22501, "zip server http listen port")
	serverHTTPSPort := flag.Int("serverHttpsPort", 22502, "zip server https listen port")
	gameRootPath := flag.String("gameRootPath", "D:\\Flashpoint 11 Infinity\\Data\\Games", "This is the path where to find the zips")
	apiPrefix := flag.String("apiPrefix", "/fpProxy/api", "apiPrefix is used to prefix any API call.")
	useMad4FP := flag.Bool("UseMad4FP", false, "flag to turn on/off Mad4FP.")
	legacyGoPort := flag.Int("legacyGoPort", 22601, "port that the legacy GO server listens on")
	legacyPHPPort := flag.Int("legacyPHPPort", 22600, "port that the legacy PHP server listens on")
	legacyPHPPath := flag.String("legacyPHPPath", "D:\\Flashpoint 11 Infinity\\Legacy", "This is the path for HTDOCS")
	legacyUsePHPServer := flag.Bool("legacyUsePHPServer", true, "This will run the original PHP script in parallel")
	legacyHTDOCSPath := flag.String("legacyHTDOCSPath", "D:\\Flashpoint 11 Infinity\\Legacy\\htdocs", "This is the path for HTDOCS")
	phpCgiPath := flag.String("phpCgiPath", "D:\\Flashpoint 11 Infinity\\Legacy\\php-cgi.exe", "Path to PHP CGI executable")
	flag.Parse()

	//Apply all of the flags to the settings
	proxySettings.VerboseLogging = *verboseLogging
	proxySettings.ProxyPort = strconv.Itoa(*proxyPort)
	proxySettings.ServerHTTPPort = strconv.Itoa(*serverHTTPPort)
	proxySettings.ServerHTTPSPort = strconv.Itoa(*serverHTTPSPort)
	proxySettings.ApiPrefix = *apiPrefix
	proxySettings.UseMad4FP = *useMad4FP
	proxySettings.LegacyGoPort = strconv.Itoa(*legacyGoPort)
	proxySettings.LegacyPHPPort = strconv.Itoa(*legacyPHPPort)
	proxySettings.LegacyPHPPath = *legacyPHPPath
	proxySettings.LegacyUsePHPServer = *legacyUsePHPServer
	proxySettings.LegacyHTDOCSPath = *legacyHTDOCSPath
	proxySettings.GameRootPath, err = filepath.Abs(strings.Trim(*gameRootPath, "\""))
	if err != nil {
		fmt.Printf("Failed to get absolute game root path")
		return
	}
	proxySettings.PhpCgiPath, err = filepath.Abs(strings.Trim(*phpCgiPath, "\""))
	if err != nil {
		fmt.Printf("Failed to get absolute php cgi path")
		return
	}

	//Setup the proxy.
	proxy = goproxy.NewProxyHttpServer()
	proxy.Verbose = proxySettings.VerboseLogging
	gamePath, _ := normalizePath("", proxySettings.GameRootPath, false)
	fmt.Printf("Proxy Server Started on port %s\n", proxySettings.ProxyPort)
	fmt.Printf("Zip Server Started\n\tHTTP Port: %s\n\tHTTPS Port: %s\n\tGame Root: %s\n",
		proxySettings.ServerHTTPPort,
		proxySettings.ServerHTTPSPort,
		gamePath)
}

func setContentType(r *http.Request, resp *http.Response) {
	if r == nil || resp == nil {
		return
	}

	rext := filepath.Ext(resp.Header.Get("ZIPSVR_FILENAME"))
	ext := filepath.Ext(r.URL.Path)
	mime := ""

	// If the request already has an extension, fetch the mime via extension
	if ext != "" {
		resp.Header.Set("Content-Type", proxySettings.ExtMimeTypes[ext[1:]])
		mime = proxySettings.ExtMimeTypes[ext[1:]]
	}

	// If the response has an extension, try and fetch the mime for that via extension
	if mime == "" && rext != "" {
		resp.Header.Set("Content-Type", proxySettings.ExtMimeTypes[rext[1:]])
		mime = proxySettings.ExtMimeTypes[rext[1:]]
	}

	// Set content type header
	resp.Header.Set("Content-Type", mime)
}

func handleRequest(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	// Remove port from host if exists (old apps don't clean it before sending requests?)
	r.URL.Host = strings.Split(r.URL.Host, ":")[0]
	fmt.Printf("Proxy Request: %s\n", r.URL.Host+r.URL.Path)

	// Copy the original request
	gamezipRequest := &http.Request{
		Method: r.Method,
		URL: &url.URL{
			Scheme:   "http",
			Host:     "127.0.0.1:" + proxySettings.ServerHTTPPort,
			Path:     "content/" + r.URL.Host + r.URL.Path,
			RawQuery: r.URL.RawQuery,
		},
		Header: make(http.Header),
		Body:   r.Body,
	}

	// Clone the body into both requests by reading and making 2 new readers
	contents, _ := ioutil.ReadAll(r.Body)
	gamezipRequest.Body = ioutil.NopCloser(bytes.NewReader(contents))

	// Make the request to the zip server.
	client := &http.Client{}
	proxyReq, err := http.NewRequest(gamezipRequest.Method, gamezipRequest.URL.String(), gamezipRequest.Body)
	if err != nil {
		fmt.Printf("UNHANDLED GAMEZIP ERROR: %s\n", err)
	}
	proxyReq.Header = gamezipRequest.Header
	proxyResp, err := client.Do(proxyReq)

	if proxyResp.StatusCode < 400 {
		fmt.Printf("\tServing from Zip...\n")
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
			Header: make(http.Header),
			Body:   r.Body,
		}
		// Copy in a new body reader
		legacyRequest.Body = ioutil.NopCloser(bytes.NewReader(contents))

		port := proxySettings.LegacyPHPPort

		// Set the Proxy URL and apply it to the Transpor layer so that the request respects the proxy.
		proxyURL, _ := url.Parse("http://127.0.0.1:" + port)
		proxy := http.ProxyURL(proxyURL)
		transport := &http.Transport{Proxy: proxy}

		// A custom Dialer is required for the "localflash" urls, instead of using the DNS, we use this.
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			//Set Dialer timeout and keepalive to 30 seconds and force the address to localhost.
			dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
			addr = "127.0.0.1:" + port
			return dialer.DialContext(ctx, network, addr)
		}

		// Make the request with the custom transport
		client := &http.Client{Transport: transport, Timeout: 300 * time.Second}
		legacyResp, err := client.Do(legacyRequest)
		// An error occured, log it for debug purposes
		if err == nil {
			fmt.Printf("\tServing from Legacy...\n")
			proxyResp = legacyResp
		} else {
			fmt.Printf("UNHANDLED LEGACY ERROR: %s\n", err)
			fmt.Printf("\tfailure legacy\n")
		}
	}

	// Update the content type based upon ext for now.
	setContentType(r, proxyResp)

	// Add extra headers
	proxyResp.Header.Set("Access-Control-Allow-Origin", "*")
	// Keep Alive
	if strings.ToLower(r.Header.Get("Connection")) == "keep-alive" {
		proxyResp.Header.Set("Connection", "Keep-Alive")
		proxyResp.Header.Set("Keep-Alive", "timeout=5; max=100")
	}

	return r, proxyResp
}

func main() {
	// To create CA cert, refer to https://wiki.mozilla.org/SecurityEngineering/x509Certs#Self_Signed_Certs
	// Replace CA in GoProxy
	certFile := "fpproxy.crt"
	keyFile := "fpproxy.key"

	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		panic(err)
	}

	goproxy.MitmConnect.TLSConfig = goproxy.TLSConfigFromCA(&cert)

	// Handle HTTPS requests (DOES NOT HANDLE HTTP)
	proxy.OnRequest().HandleConnect(goproxy.AlwaysMitm)
	proxy.OnRequest().HijackConnect(func(req *http.Request, client net.Conn, ctx *goproxy.ProxyCtx) {
		_, resp := handleRequest(req, ctx)
		resp.Write(client)
		client.Close()
	})

	// Handle HTTP requests (DOES NOT HANDLE HTTPS)
	proxy.OnRequest().DoFunc(func(r *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
		return handleRequest(r, ctx)
	})

	//Start ZIP server
	go func() {
		//TODO: Update these to be modifiable in the properties json.
		//TODO: Also update the "fpProxy/api/" to be in the properties json.
		log.Fatal(http.ListenAndServe("127.0.0.1:"+proxySettings.ServerHTTPPort,
			zipfs.EmptyFileServer(
				proxySettings.ApiPrefix,
				"",
				proxySettings.VerboseLogging,
				proxySettings.ExtIndexTypes,
				proxySettings.GameRootPath,
				proxySettings.PhpCgiPath,
				proxySettings.ExtMimeTypes,
				proxySettings.OverridePaths,
			),
		))
	}()

	/** THIS SOFTWARE DOES NOT CONTROL THE PHP ROUTER LIFECYCLE */
	// //Start Legacy server
	// go func() {
	// 	if proxySettings.LegacyUsePHPServer {
	// 		runLegacyPHP()
	// 	} else {
	// 		log.Fatal(http.ListenAndServe("127.0.0.1:"+proxySettings.LegacyGoPort, getLegacyProxy()))
	// 	}
	// }()

	//Start PROXY server
	log.Fatal(http.ListenAndServe("127.0.0.1:"+proxySettings.ProxyPort, http.AllowQuerySemicolons(proxy)))
}

func runLegacyPHP() {
	phpPath := filepath.Join(proxySettings.LegacyPHPPath, "php")
	cmd := exec.Command(phpPath, "-S", "127.0.0.1:"+proxySettings.LegacyPHPPort, "router.php")
	cmd.Dir = proxySettings.LegacyPHPPath
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	cmd.Start()

	c := make(chan os.Signal, 2)
	signal.Notify(c, os.Interrupt, os.Kill)
	go func() {
		<-c
		// cleanup
		cmd.Process.Kill()
		os.Exit(1)
	}()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		s := bufio.NewScanner(stdout)
		for s.Scan() {
			fmt.Println(s.Text())
		}
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		s := bufio.NewScanner(stderr)
		for s.Scan() {
			fmt.Println(s.Text())
		}
		wg.Done()
	}()

	wg.Wait()
}

func serveOverrideFile(w http.ResponseWriter, r *http.Request) {

}
