package zipfs

// Some of the functions in this file are adapted from private
// functions in the standard library net/http package.
//
// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// FileServer returns a HTTP handler that serves
// HTTP requests with the contents of the ZIP file system.
// It provides slightly better performance than the
// http.FileServer implementation because it serves compressed content
// to clients that can accept the "deflate" compression algorithm.
func FileServer(fs *FileSystem, baseAPIPath string, urlPrepend string, isVerbose bool, indexExts []string, mimeExts map[string]string) http.Handler {
	fsVal := []*FileSystem{fs}
	h := &ZipFileServer{
		fs:          fsVal,
		baseAPIPath: baseAPIPath,
		isVerbose:   isVerbose,
		urlPrepend:  urlPrepend,
		indexExts:   indexExts,
		mimeExts:    mimeExts,
	}

	return h
}

func FileServers(fs []*FileSystem, baseAPIPath string, urlPrepend string, isVerbose bool, indexExts []string, mimeExts map[string]string) http.Handler {
	h := &ZipFileServer{
		fs:          fs,
		baseAPIPath: baseAPIPath,
		isVerbose:   isVerbose,
		urlPrepend:  urlPrepend,
		indexExts:   indexExts,
		mimeExts:    mimeExts,
	}

	return h
}

func EmptyFileServer(baseAPIPath string, urlPrepend string, isVerbose bool, indexExts []string, baseMountDir string, phpPath string, mimeExts map[string]string, overrideBases []string, htdocsPath string) http.Handler {
	return &ZipFileServer{
		baseAPIPath:   baseAPIPath,
		isVerbose:     isVerbose,
		urlPrepend:    urlPrepend,
		indexExts:     indexExts,
		baseMountDir:  baseMountDir,
		phpPath:       phpPath,
		mimeExts:      mimeExts,
		overrideBases: overrideBases,
		htdocsPath:    htdocsPath,
	}
}

type ZipFileServer struct {
	fs            []*FileSystem
	baseAPIPath   string
	isVerbose     bool
	urlPrepend    string
	indexExts     []string
	baseMountDir  string
	phpPath       string
	mimeExts      map[string]string
	overrideBases []string
	htdocsPath    string
}

type Mount struct {
	FilePath string `json:"filePath"`
}

type MountList struct {
	MountedZips []string `json:"mountedZips"`
}

func (h *ZipFileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var urlPath = path.Join("/", strings.ToLower(r.URL.Path))
	var basePath = strings.ToLower(h.baseAPIPath)

	if urlPath == path.Join("/", basePath, "/mountzip") {
		h.MountFs(w, r)
		return
	}

	if urlPath == path.Join("/", basePath, "/unmountzip") {
		h.UnMountFs(w, r)
		return
	}

	if urlPath == path.Join("/", basePath, "/listmountzip") {
		h.ListMountedFs(w, r)
		return
	}

	upath := r.URL.Path
	if !strings.HasPrefix(upath, "/") {
		upath = "/" + upath
		r.URL.Path = upath
	}
	serveFiles(w, r, h, path.Clean(upath), true, h.phpPath)
}

// Add a ZIP file at runtime.
func (h *ZipFileServer) MountFs(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		fmt.Printf("Error (MountFs): Invalid request, not a POST")
		http.Error(w, "POST request expected.", http.StatusBadRequest)
		return
	}

	var m Mount
	err := json.NewDecoder(r.Body).Decode(&m)
	if err != nil {
		fmt.Printf("Error (MountFs): %s\n", err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Ensure the zip is within the base directory
	var zipPath string
	if filepath.IsAbs(m.FilePath) {
		zipPath = path.Clean(m.FilePath)
	} else {
		zipPath = path.Join(h.baseMountDir, m.FilePath)
		zipPath = path.Clean(zipPath)
	}
	if !strings.HasPrefix(zipPath, h.baseMountDir) {
		fmt.Printf("Error (MountFs): Illegal path access (%s) %s", m.FilePath, zipPath)
		http.Error(w, "Illegal path access", http.StatusBadRequest)
		return
	}

	// Prevent duplicate mounts
	for _, fse := range h.fs {
		if fse.givenPath == zipPath {
			fmt.Printf("Error (MountFs): Zip already mounted (%s) %s", m.FilePath, zipPath)
			makeJsonResponse(w, SimpleResponseData{
				Message: "Zip file already mounted!",
			}, http.StatusOK)
			return
		}
	}

	fmt.Printf("Mounting Zip: %s\n", zipPath)
	newFS, fpErr := New(zipPath)
	if fpErr != nil {
		fmt.Printf("Error (MountFs): %s\n", fpErr.Error())
		http.Error(w, fpErr.Error(), http.StatusNotFound)
		return
	}

	// Find all files ending with a script extension and copy them to htdocs - Assists with file related PHP calls to other PHP files
	count := 0
	for _, f := range newFS.fileInfos {
		if checkForPhp(f.name) {
			extractPath := path.Clean(path.Join(h.htdocsPath, strings.TrimLeft(f.name, "content/")))
			if h.isVerbose {
				fmt.Printf("Extracting PHP file: %s\n", extractPath)
			}

			// Create the destination directory
			err := os.MkdirAll(filepath.Dir(extractPath), os.ModePerm)
			if err != nil {
				fmt.Printf("Error (MountFs): %s\n", err.Error())
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			// Open the file to write to
			outFile, err := os.Create(extractPath)
			if err != nil {
				fmt.Printf("Error (MountFs) - Failed to make HTDOCS Folder: %s\n", err.Error())
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer outFile.Close()

			// Open PHP file from Zip and copy
			reader, err := f.zipFile.Open()
			if err != nil {
				fmt.Printf("Error (MountFs) - Failed to open Zipped file content: %s\n", err.Error())
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer reader.Close()

			_, err = io.Copy(outFile, reader)
			if err != nil {
				fmt.Printf("Error (MountFs) - Failed to copy Zipped file content: %s\n", err.Error())
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			count++
		}
	}
	if count > 0 {
		fmt.Printf("Extracted %d PHP files to %s\n", count, h.htdocsPath)
	}

	if h.isVerbose {
		fmt.Printf("Zip Mounted: %s\n", zipPath)
	}

	h.fs = append(h.fs, newFS)
	makeJsonResponse(w, SimpleResponseData{
		Message: "Zip file mounted!",
	}, http.StatusOK)
	return
}

// Remove a ZIP file at runtime.
func (h *ZipFileServer) UnMountFs(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		fmt.Printf("Error (UnMountFs): Invalid request, not a POST\n")
		http.Error(w, "POST request expected.", http.StatusBadRequest)
		return
	}

	var m Mount
	err := json.NewDecoder(r.Body).Decode(&m)
	if err != nil {
		fmt.Printf("Error (UnMountFs): %s\n", err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Ensure the zip is within the base directory
	var zipPath string
	if filepath.IsAbs(m.FilePath) {
		zipPath = path.Clean(m.FilePath)
	} else {
		zipPath = path.Join(h.baseMountDir, m.FilePath)
		zipPath = path.Clean(zipPath)
	}
	if !strings.HasPrefix(zipPath, h.baseMountDir) {
		fmt.Printf("Error (MountFs): Illegal path access (%s) %s", m.FilePath, zipPath)
		http.Error(w, "Illegal path access", http.StatusBadRequest)
		return
	}

	//Loop through and remove the zip requested
	fmt.Printf("UnMounting Zip: %s\n", zipPath)
	var found = false
	for i := len(h.fs) - 1; i >= 0; i-- {
		if h.fs[i].givenPath == zipPath {
			found = true
			err := h.fs[i].Close()
			if err != nil {
				fmt.Printf("Failed to close zip file %s: %s\n", zipPath, err)
			}
			h.fs = append(h.fs[:i], h.fs[i+1:]...)
		}
	}

	if found && h.isVerbose {
		fmt.Printf("Zip UnMounted: %s\n", zipPath)
	}

	makeJsonResponse(w, SimpleResponseData{
		Message: "Zip file unmounted!",
	}, http.StatusOK)
	return
}

// Remove a ZIP file at runtime.
func (h *ZipFileServer) ListMountedFs(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		fmt.Printf("Error (ListMountedFs): Invalid request, not a GET\n")
		http.Error(w, "GET request expected.", http.StatusBadRequest)
		return
	}

	var ml MountList
	for _, fse := range h.fs {
		ml.MountedZips = append(ml.MountedZips, fse.givenPath)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ml)
	return
}

// name is '/'-separated, not filepath.Separator.
func serveFiles(w http.ResponseWriter, r *http.Request, h *ZipFileServer, name string, redirect bool, phpPath string) {
	//If a file is attempting to be served, but no zips are available
	//We want to fail gracefully.
	const indexPage = "/index.html"

	// redirect .../index.html to .../
	// can't use Redirect() because that would make the path absolute,
	// which would be a problem running under StripPrefix
	if strings.HasSuffix(r.URL.Path, indexPage) {
		localRedirect(w, r, "./")
		return
	}

	var fi *fileInfo
	var fsVal *FileSystem
	var errVal error

	var errMsg string
	var errCode int
	var errFlag = false

	// Check for file overrides
	for _, overrideBase := range h.overrideBases {
		cleanName, _ := url.PathUnescape(strings.ToLower(path.Clean(name)))
		trimmedName := strings.TrimLeft(cleanName, "/")
		// Remove content prefix, not needed when querying local files
		if strings.HasPrefix(trimmedName, "content/") {
			trimmedName = trimmedName[len("content/"):]
		}

		localFile := path.Join(overrideBase, trimmedName)

		stats, err := os.Stat(localFile)
		if err != nil {
			continue
		}

		var foundFile *os.File = nil

		if stats.IsDir() {
			for _, extension := range h.indexExts {
				// use contents of index.html for directory, if present
				index := path.Join(strings.TrimPrefix(localFile, "/"), "/index."+extension)
				file, err := os.Open(index)
				if err == nil {
					foundFile = file
					defer foundFile.Close()
					break
				}
			}
		} else {
			file, err := os.Open(localFile)
			if err == nil {
				foundFile = file
				defer foundFile.Close()
			}
		}

		// Found a file, return it
		if foundFile != nil {
			//Now that we have a file, override the mime-type if it on the list
			mimeOverride, ok := h.mimeExts[strings.ToLower(filepath.Ext(path.Base(foundFile.Name())))]
			if ok {
				w.Header().Set("Content-Type", mimeOverride)
			}

			// serveContent will check modification time and ETag
			w.Header().Set("ZIPSVR_FILENAME", foundFile.Name())
			stats, err := os.Stat(foundFile.Name())
			if err != nil {
				continue
			}
			fmt.Printf("Serving override file: %s\n", foundFile.Name())
			http.ServeContent(w, r, foundFile.Name(), stats.ModTime(), foundFile)
			return
		}
	}

	if len(h.fs) == 0 {
		http.Error(w, "File not found, no ZIP is added.", http.StatusNotFound)
		return
	}

	// Loop through the files in order to find the first match
	for _, fse := range h.fs {
		errFlag = false
		errVal = nil
		fii, err := fse.openFileInfo(name)
		if err != nil {
			errVal = err
		}
		fsVal = fse
		fi = fii

		//If we did not find a file above it will note the error and
		//move onto the next zip to see if the file is there.
		if errVal != nil {
			errFlag = true
			errMsg, errCode = toHTTPError(errVal)
			continue
		}

		if redirect {
			// redirect to canonical path: / at end of directory url
			// r.URL.Path always begins with /
			url := r.URL.Path
			if fi.IsDir() {
				if url[len(url)-1] != '/' {
					localRedirect(w, r, path.Base(url)+"/")
					return
				}
			} else {
				if url[len(url)-1] == '/' {
					localRedirect(w, r, "../"+path.Base(url))
					return
				}
			}
		}

		//Loop through all available extensions and attempt to open them.
		if fi.IsDir() {
			for _, extension := range h.indexExts {
				// use contents of index.html for directory, if present
				index := path.Join(strings.TrimPrefix(name, "/"), "/index."+extension)
				fii, err := fsVal.openFileInfo(index)
				if err == nil {
					fi = fii
				}
			}
		}

		// Still a directory? (we didn't find an index.html file)
		if fi.IsDir() {
			// Unlike the standard library implementation, directory
			// listing is prohibited.
			errFlag = true
			errMsg = "Forbidden"
			errCode = http.StatusForbidden
			continue
		}

		//Now that we have a file, override the mime-type if it on the list
		mimeOverride, ok := h.mimeExts[strings.ToLower(filepath.Ext(path.Base(fi.Name())))]
		if ok {
			w.Header().Set("Content-Type", mimeOverride)
		}

		// serveContent will check modification time and ETag
		w.Header().Set("ZIPSVR_FILENAME", fi.name)

		//If the default value exists, send it over to be used, otherwise use default functionality.
		mimeDefaultOverride, defExists := h.mimeExts["default"]
		if defExists {
			serveContent(w, r, fsVal, fi, phpPath, h.htdocsPath, &mimeDefaultOverride)
		} else {
			serveContent(w, r, fsVal, fi, phpPath, h.htdocsPath, nil)
		}
		return
	}

	if errFlag {
		http.Error(w, errMsg, errCode)
		return
	}
}

func serveContent(w http.ResponseWriter, r *http.Request, fs *FileSystem, fi *fileInfo, phpPath string, htdocsPath string, defaultMime *string) {
	if checkLastModified(w, r, fi.ModTime()) {
		return
	}

	// Set the Etag header in the response before calling checkETag.
	// The checkETag function obtains the files ETag from the response header.
	w.Header().Set("Etag", calcEtag(fi.zipFile))
	rangeReq, done := checkETag(w, r, fi.ModTime())
	if done {
		return
	}
	if rangeReq != "" {
		// Range request requires seeking, so at this point create a temporary
		// file and let the standard library serve it.
		f := fi.openReader(r.URL.Path)
		defer f.Close()
		f.createTempFile()
		http.ServeContent(w, r, fi.Name(), fi.ModTime(), f.file)
		return
	}

	setContentType(w, fi.Name(), defaultMime)

	switch fi.zipFile.Method {
	case zip.Deflate:
		fallthrough
	case zip.Store:
		serveIdentity(w, r, fi, phpPath, htdocsPath)
	default:
		http.Error(w, fmt.Sprintf("unsupported zip method: %d", fi.zipFile.Method), http.StatusInternalServerError)
	}
}

// serveIdentity serves a zip file in identity content encoding .
func serveIdentity(w http.ResponseWriter, r *http.Request, fi *fileInfo, phpPath string, htdocsPath string) {
	// TODO: need to check if the client explicitly refuses to accept
	// identity encoding (Accept-Encoding: identity;q=0), but this is
	// going to be very rare.

	// Divert php requests
	if phpPath != "" && checkForPhp(fi.name) {
		fileName := strings.TrimLeft(fi.name, "content/")
		// Run the file from the htdocs directory instead
		htdocsFile := path.Clean(path.Join(htdocsPath, fileName))
		fmt.Printf("Executing PHP Script: %s\n", fileName)
		Cgi(w, r, phpPath, htdocsFile)
		return
	}

	zf := fi.zipFile
	reader, err := zf.Open()
	if err != nil {
		msg, code := toHTTPError(err)
		http.Error(w, msg, code)
		return
	}
	defer reader.Close()

	size := zf.FileInfo().Size()
	w.Header().Del("Content-Encoding")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	if r.Method != "HEAD" {
		io.CopyN(w, reader, size)
	}
	fmt.Printf("[Zipfs] Serving Zipped File: %s\n", zf.Name)
}

// serveDeflat serves a zip file in deflate content-encoding if the
// user agent can accept it. Otherwise it calls serveIdentity.
func serveDeflate(w http.ResponseWriter, r *http.Request, fi *fileInfo, readerAt io.ReaderAt, phpPath string, htdocsPath string) {
	acceptEncoding := r.Header.Get("Accept-Encoding")

	// TODO: need to parse the accept header to work out if the
	// client is explicitly forbidding deflate (ie deflate;q=0)
	acceptsDeflate := strings.Contains(acceptEncoding, "deflate")
	if !acceptsDeflate {
		// client will not accept deflate, so serve as identity
		serveIdentity(w, r, fi, phpPath, htdocsPath)
		return
	}

	f := fi.zipFile
	contentLength := int64(f.CompressedSize64)
	if contentLength == 0 {
		contentLength = int64(f.CompressedSize64)
	}
	w.Header().Set("Content-Encoding", "deflate")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", contentLength))
	if r.Method == "HEAD" {
		return
	}

	var written int64
	remaining := contentLength
	offset, err := f.DataOffset()
	if err != nil {
		msg, code := toHTTPError(err)
		http.Error(w, msg, code)
		return
	}

	// re-use buffers to reduce stress on GC
	buf := bufPool.Get()
	defer bufPool.Free(buf)

	// loop to write the raw deflated content to the client
	for remaining > 0 {
		size := len(buf)
		if int64(size) > remaining {
			size = int(remaining)
		}

		b := buf[:size]
		_, err := readerAt.ReadAt(b, offset)
		if err != nil {
			if written == 0 {
				// have not written anything to the client yet, so we can send an error
				msg, code := toHTTPError(err)
				http.Error(w, msg, code)
			}
			return
		}
		if _, err := w.Write(b); err != nil {
			// Cannot write an error to the client because, er,  we just
			// failed to write to the client.
			return
		}
		written += int64(size)
		remaining -= int64(size)
		offset += int64(size)
	}
}

func setContentType(w http.ResponseWriter, filename string, defaultMime *string) {
	ctypes, haveType := w.Header()["Content-Type"]
	var ctype string

	if !haveType {
		ctype = mime.TypeByExtension(filepath.Ext(path.Base(filename)))
		if ctype == "" {
			// the standard library sniffs content to decide whether it is
			// binary or text, but this requires a ReaderSeeker, and we
			// only have a reader from the zip file. Assume binary.
			// unless the default mime is overridden, then use that!
			if defaultMime == nil {
				ctype = "application/octet-stream"
			} else {
				ctype = *defaultMime
			}
		}
	} else if len(ctypes) > 0 {
		ctype = ctypes[0]
	}

	if ctype != "" {
		w.Header().Set("Content-Type", ctype)
	}
}

// calcEtag calculates an ETag value for a given zip file based on
// the file's CRC and its length.
func calcEtag(f *zip.File) string {
	size := f.UncompressedSize64
	if size == 0 {
		size = f.UncompressedSize64
	}
	etag := uint64(f.CRC32) ^ (size & 0xffffffff << 32)

	// etag should always be in double quotes
	return fmt.Sprintf(`"%x"`, etag)
}

var unixEpochTime = time.Unix(0, 0)

// modtime is the modification time of the resource to be served, or IsZero().
// return value is whether this request is now complete.
func checkLastModified(w http.ResponseWriter, r *http.Request, modtime time.Time) bool {
	if modtime.IsZero() || modtime.Equal(unixEpochTime) {
		// If the file doesn't have a modtime (IsZero), or the modtime
		// is obviously garbage (Unix time == 0), then ignore modtimes
		// and don't process the If-Modified-Since header.
		return false
	}

	// The Date-Modified header truncates sub-second precision, so
	// use mtime < t+1s instead of mtime <= t to check for unmodified.
	if t, err := time.Parse(http.TimeFormat, r.Header.Get("If-Modified-Since")); err == nil && modtime.Before(t.Add(1*time.Second)) {
		h := w.Header()
		delete(h, "Content-Type")
		delete(h, "Content-Length")
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	w.Header().Set("Last-Modified", modtime.UTC().Format(http.TimeFormat))
	return false
}

// checkETag implements If-None-Match and If-Range checks.
//
// The ETag or modtime must have been previously set in the
// ResponseWriter's headers.  The modtime is only compared at second
// granularity and may be the zero value to mean unknown.
//
// The return value is the effective request "Range" header to use and
// whether this request is now considered done.
func checkETag(w http.ResponseWriter, r *http.Request, modtime time.Time) (rangeReq string, done bool) {
	etag := w.Header().Get("Etag")
	rangeReq = r.Header.Get("Range")

	// Invalidate the range request if the entity doesn't match the one
	// the client was expecting.
	// "If-Range: version" means "ignore the Range: header unless version matches the
	// current file."
	// We only support ETag versions.
	// The caller must have set the ETag on the response already.
	if ir := r.Header.Get("If-Range"); ir != "" && ir != etag {
		// The If-Range value is typically the ETag value, but it may also be
		// the modtime date. See golang.org/issue/8367.
		timeMatches := false
		if !modtime.IsZero() {
			if t, err := http.ParseTime(ir); err == nil && t.Unix() == modtime.Unix() {
				timeMatches = true
			}
		}
		if !timeMatches {
			rangeReq = ""
		}
	}

	if inm := r.Header.Get("If-None-Match"); inm != "" {
		// Must know ETag.
		if etag == "" {
			return rangeReq, false
		}

		// TODO(bradfitz): non-GET/HEAD requests require more work:
		// sending a different status code on matches, and
		// also can't use weak cache validators (those with a "W/
		// prefix).  But most users of ServeContent will be using
		// it on GET or HEAD, so only support those for now.
		if r.Method != "GET" && r.Method != "HEAD" {
			return rangeReq, false
		}

		// TODO(bradfitz): deal with comma-separated or multiple-valued
		// list of If-None-match values.  For now just handle the common
		// case of a single item.
		if inm == etag || inm == "*" {
			h := w.Header()
			delete(h, "Content-Type")
			delete(h, "Content-Length")
			w.WriteHeader(http.StatusNotModified)
			return "", true
		}
	}
	return rangeReq, false
}

// toHTTPError returns a non-specific HTTP error message and status code
// for a given non-nil error value. It's important that toHTTPError does not
// actually return err.Error(), since msg and httpStatus are returned to users,
// and historically Go's ServeContent always returned just "404 Not Found" for
// all errors. We don't want to start leaking information in error messages.
func toHTTPError(err error) (msg string, httpStatus int) {
	if pathErr, ok := err.(*os.PathError); ok {
		err = pathErr.Err
	}
	if os.IsNotExist(err) {
		return "404 page not found", http.StatusNotFound
	}
	if os.IsPermission(err) {
		return "403 Forbidden", http.StatusForbidden
	}
	// Default:
	return "500 Internal Server Error", http.StatusInternalServerError
}

// localRedirect gives a Moved Permanently response.
// It does not convert relative paths to absolute paths like Redirect does.
func localRedirect(w http.ResponseWriter, r *http.Request, newPath string) {
	if q := r.URL.RawQuery; q != "" {
		newPath += "?" + q
	}
	w.Header().Set("Location", newPath)
	w.WriteHeader(http.StatusMovedPermanently)
}

type SimpleResponseData struct {
	Message string `json:"msg"`
}

func makeJsonResponse(w http.ResponseWriter, data interface{}, status int) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(data)
}

func checkForPhp(filePath string) bool {
	suffixes := []string{".php", ".phtml", ".php5"}

	for _, suffix := range suffixes {
		if strings.HasSuffix(filePath, suffix) {
			return true
		}
	}

	return false
}
