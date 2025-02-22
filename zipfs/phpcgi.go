package zipfs

import (
	"io"
	"net/http"
	"net/http/cgi"
	"os"
)

func Cgi(w http.ResponseWriter, r *http.Request, phpBin string, scriptFileName string) {
	handler := new(cgi.Handler)
	handler.Path = phpBin
	handler.Env = append(handler.Env, "REDIRECT_STATUS=CGI")
	handler.Env = append(handler.Env, "SCRIPT_FILENAME="+scriptFileName)

	// Unchunk the request body when required
	if len(r.TransferEncoding) > 0 && r.TransferEncoding[0] == "chunked" {
		// Remove the chunked marker
		r.TransferEncoding = r.TransferEncoding[1:]
	}

	if r.Body != nil {
		// Save body into temp file (don't want large request bodies eating memory)
		tmpfile, err := os.CreateTemp("", "fp-cgi-")
		if err != nil {
			http.Error(w, "Error creating temp file for request body", http.StatusInternalServerError)
			return
		}
		defer os.Remove(tmpfile.Name())

		// Write the body to the temp file
		size, err := io.Copy(tmpfile, r.Body)
		if err != nil {
			http.Error(w, "Error writing request body to temp file", http.StatusInternalServerError)
			return
		}

		// Move seek to start of file
		_, err = tmpfile.Seek(0, 0)
		if err != nil {
			http.Error(w, "Error seeking to start of temp file", http.StatusInternalServerError)
			return
		}

		r.Body = tmpfile
		r.ContentLength = size
	}

	handler.ServeHTTP(w, r)
}
