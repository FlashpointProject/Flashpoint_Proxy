package main

import (
	"net/http"
	"os"
	"testing"

	"github.com/FlashpointProject/FlashpointGameServer/zipfs"
)

var zipHandler http.Handler

func TestMain(m *testing.M) {
	initServer()
	zipHandler = zipfs.EmptyFileServer(
		serverSettings.ApiPrefix,
		"",
		serverSettings.VerboseLogging,
		serverSettings.ExtIndexTypes,
		"./",
		serverSettings.PhpCgiPath,
		serverSettings.ExtMimeTypes,
		serverSettings.OverridePaths,
		serverSettings.LegacyHTDOCSPath,
	)
	code := m.Run()
	os.Exit(code)
}

func BenchmarkLoadZip(b *testing.B) {

}
