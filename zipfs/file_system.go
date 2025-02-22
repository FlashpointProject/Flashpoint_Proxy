package zipfs

import (
	"archive/zip"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

var (
	errFileClosed       = errors.New("file closed")
	errFileSystemClosed = errors.New("filesystem closed")
	errNotDirectory     = errors.New("not a directory")
	errDirectory        = errors.New("is a directory")
)

// List of encoders in order of most common to least common
// for zips, first one found stops.
var charmapEncoders []*encoding.Encoder = []*encoding.Encoder{
	charmap.CodePage437.NewEncoder(),
	unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewEncoder(),
	unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM).NewEncoder(),
	charmap.ISO8859_1.NewEncoder(),
	charmap.Windows1251.NewEncoder(),
	charmap.Windows1252.NewEncoder(),
	japanese.ShiftJIS.NewEncoder(),
	simplifiedchinese.GBK.NewEncoder(),
	charmap.ISO8859_8.NewEncoder(),
	charmap.Windows1254.NewEncoder(),
	japanese.EUCJP.NewEncoder(),
	traditionalchinese.Big5.NewEncoder(),
	simplifiedchinese.HZGB2312.NewEncoder(),
	korean.EUCKR.NewEncoder(),
	charmap.Windows874.NewEncoder(),
	charmap.Windows1250.NewEncoder(),
	charmap.Windows1253.NewEncoder(),
	charmap.Windows1255.NewEncoder(),
	charmap.Windows1256.NewEncoder(),
	charmap.Windows1257.NewEncoder(),
	charmap.Windows1258.NewEncoder(),
	charmap.ISO8859_2.NewEncoder(),
	charmap.ISO8859_5.NewEncoder(),
	charmap.ISO8859_7.NewEncoder(),
	charmap.ISO8859_8.NewEncoder(),
	japanese.ISO2022JP.NewEncoder(),
	simplifiedchinese.GB18030.NewEncoder(),
	charmap.Macintosh.NewEncoder(),
	charmap.MacintoshCyrillic.NewEncoder(),
	charmap.CodePage855.NewEncoder(),
	charmap.CodePage866.NewEncoder(),
}

// FileSystem is a file system based on a ZIP file.
// It implements the http.FileSystem interface.
type FileSystem struct {
	readerAt  io.ReaderAt
	closer    io.Closer
	reader    *zip.Reader
	fileInfos fileInfoMap
	givenPath string
	fullPath  string
}

// New will open the Zip file specified by name and
// return a new FileSystem based on that Zip file.
func New(name string) (*FileSystem, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	fi, err := file.Stat()
	if err != nil {
		return nil, err
	}
	return NewFromReaderAt(file, fi.Size(), file, name)
}

// NewFromReaderAt will open the Zip file accessible by readerAt with the given size.
// The closer, if not nil, will be called when the file system is closed.
func NewFromReaderAt(readerAt io.ReaderAt, size int64, closer io.Closer, filePath string) (*FileSystem, error) {
	zipReader, err := zip.NewReader(readerAt, size)
	if err != nil {
		return nil, err
	}

	// Separate the file into an io.ReaderAt and an io.Closer.
	// Earlier versions of the code allowed for opening a filesystem
	// just with an io.ReaderAt. Not also that thw zip.Reader is
	// not actually used outside of this function so it probably
	// does not need to be in the FileSystem structure. Keeping it
	// there for now but may remove it in future.
	workingDir, _ := os.Getwd()
	fs := &FileSystem{
		closer:    closer,
		readerAt:  readerAt,
		reader:    zipReader,
		fileInfos: fileInfoMap{},
		givenPath: filePath,
		fullPath:  path.Join(workingDir, filePath),
	}

	// Build a map of file paths to speed lookup.
	// Note that this assumes that there are not a very
	// large number of files in the ZIP file.
	//
	// Because we iterate through the map it seems reasonable
	// to attach each fileInfo to it's parent directory. Once again,
	// reasonable if the ZIP file does not contain a very large number
	// of entries.
	for _, zf := range fs.reader.File {
		fi := fs.fileInfos.FindOrCreate(zf.Name)
		fi.zipFile = zf
		fiParent := fs.fileInfos.FindOrCreateParent(zf.Name)
		fiParent.fileInfos = append(fiParent.fileInfos, fi)
	}

	// Sort all of the list of fileInfos in each directory.
	for _, fi := range fs.fileInfos {
		if len(fi.fileInfos) > 1 {
			sort.Sort(fi.fileInfos)
		}
	}

	return fs, nil
}

// Open implements the http.FileSystem interface.
// A http.File is returned, which can be served by
// the http.FileServer implementation.
func (fs *FileSystem) Open(name string) (http.File, error) {
	fi, err := fs.openFileInfo(name)
	if err != nil {
		return nil, err
	}

	return fi.openReader(name), nil
}

// Close closes the file system's underlying ZIP file and
// releases all memory allocated to internal data structures.
func (fs *FileSystem) Close() error {
	fs.reader = nil
	fs.readerAt = nil
	var err error
	if fs.closer != nil {
		err = fs.closer.Close()
		fs.closer = nil
	}
	fs.fileInfos = nil
	return err
}

type fileInfoList []*fileInfo

func (fl fileInfoList) Len() int {
	return len(fl)
}

func (fl fileInfoList) Less(i, j int) bool {
	name1 := fl[i].Name()
	name2 := fl[j].Name()
	return name1 < name2
}

func (fl fileInfoList) Swap(i, j int) {
	fi := fl[i]
	fl[i] = fl[j]
	fl[j] = fi
}

func (fs *FileSystem) openFileInfo(name string) (*fileInfo, error) {
	if fs.readerAt == nil {
		return nil, errFileSystemClosed
	}
	name, _ = url.PathUnescape(strings.ToLower(path.Clean(name)))
	trimmedName := strings.TrimLeft(name, "/")

	//Check if the UTF-8 or ASCII name exists
	fi := fs.fileInfos[trimmedName]
	if fi == nil {
		//Check if any of the other codes exist
		fi = fs.testAltEncodings(name)
		//If no Codes still exist, return nil with Error
		if fi == nil {
			return nil, &os.PathError{Op: "Open", Path: name, Err: os.ErrNotExist}
		}
	}

	return fi, nil
}

func (fs *FileSystem) testAltEncodings(name string) *fileInfo {
	for _, enc := range charmapEncoders {
		strVal, err := transformEncoding(strings.NewReader(name), enc)
		if err != nil {
			continue
		}

		name = strings.ToLower(path.Clean(strVal))
		fi := fs.fileInfos[name]
		if fi != nil {
			return fi
		}
	}

	return nil
}

func transformEncoding(rawReader io.Reader, trans transform.Transformer) (string, error) {
	ret, err := ioutil.ReadAll(transform.NewReader(rawReader, trans))
	if err == nil {
		return string(ret), nil
	} else {
		return "", err
	}
}

// fileMap keeps track of fileInfos
type fileInfoMap map[string]*fileInfo

func (fm fileInfoMap) FindOrCreate(name string) *fileInfo {
	name = strings.ToLower(name)
	strippedName := strings.TrimRight(name, "/")
	fi := fm[name]
	if fi == nil {
		fi = &fileInfo{
			name: name,
		}
		fm[name] = fi
		if strippedName != name {
			// directories get two entries: with and without trailing slash
			fm[strippedName] = fi
		}
	}
	return fi
}

func (fm fileInfoMap) FindOrCreateParent(name string) *fileInfo {
	strippedName := strings.TrimRight(name, "/")
	dirName := path.Dir(strippedName)
	if dirName == "." {
		dirName = "/"
	} else if !strings.HasSuffix(dirName, "/") {
		dirName = dirName + "/"
	}
	return fm.FindOrCreate(dirName)
}

// fileInfo implements the os.FileInfo interface.
type fileInfo struct {
	name      string
	fs        *FileSystem
	zipFile   *zip.File
	fileInfos fileInfoList
	tempPath  string
	mutex     sync.Mutex
}

func (fi *fileInfo) Name() string {
	return path.Base(fi.name)
}

func (fi *fileInfo) Size() int64 {
	if fi.zipFile == nil {
		return 0
	}
	if fi.zipFile.UncompressedSize64 == 0 {
		return int64(fi.zipFile.UncompressedSize)
	}
	return int64(fi.zipFile.UncompressedSize64)
}

func (fi *fileInfo) Mode() os.FileMode {
	if fi.zipFile == nil || fi.IsDir() {
		return 0555 | os.ModeDir
	}
	return 0444
}

var dirTime = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

func (fi *fileInfo) ModTime() time.Time {
	if fi.zipFile == nil {
		return dirTime
	}
	return fi.zipFile.ModTime()
}

func (fi *fileInfo) IsDir() bool {
	if fi.zipFile == nil {
		return true
	}
	return fi.zipFile.Mode().IsDir()
}

func (fi *fileInfo) Sys() interface{} {
	return fi.zipFile
}

func (fi *fileInfo) openReader(name string) *fileReader {
	return &fileReader{
		fileInfo: fi,
		name:     name,
	}
}

func (fi *fileInfo) readdir() ([]os.FileInfo, error) {
	if !fi.Mode().IsDir() {
		return nil, errNotDirectory
	}

	v := make([]os.FileInfo, len(fi.fileInfos))
	for i, fi := range fi.fileInfos {
		v[i] = fi
	}
	return v, nil
}

type fileReader struct {
	name     string // the name used to open
	fileInfo *fileInfo
	reader   io.ReadCloser
	file     *os.File
	closed   bool
	readdir  []os.FileInfo
}

func (f *fileReader) Close() error {
	var errs []error
	if f.reader != nil {
		err := f.reader.Close()
		errs = append(errs, err)
	}
	var tempFile string
	if f.file != nil {
		tempFile = f.file.Name()
		err := f.file.Close()
		errs = append(errs, err)
	}
	if tempFile != "" {
		err := os.Remove(tempFile)
		errs = append(errs, err)
	}

	f.closed = true

	for _, err := range errs {
		if err != nil {
			return f.pathError("Close", err)
		}
	}
	return nil
}

func (f *fileReader) Read(p []byte) (n int, err error) {
	if f.closed {
		return 0, f.pathError("Read", errFileClosed)
	}
	if f.file != nil {
		return f.file.Read(p)
	}
	if f.reader == nil {
		f.reader, err = f.fileInfo.zipFile.Open()
		if err != nil {
			return 0, err
		}
	}
	return f.reader.Read(p)
}

func (f *fileReader) Seek(offset int64, whence int) (int64, error) {
	if f.closed {
		return 0, f.pathError("Seek", errFileClosed)
	}

	// The reader cannot seek, so close it.
	if f.reader != nil {
		if err := f.reader.Close(); err != nil {
			return 0, err
		}
	}

	// A special case for when there is no file created and the seek is
	// to the beginning of the file. Just open (or re-open) the reader
	// at the beginning of the file.
	if f.file == nil && offset == 0 && whence == 0 {
		var err error
		f.reader, err = f.fileInfo.zipFile.Open()
		return 0, err
	}

	if err := f.createTempFile(); err != nil {
		return 0, err
	}

	return f.file.Seek(offset, whence)
}

func (f *fileReader) Readdir(count int) ([]os.FileInfo, error) {
	var err error
	var osFileInfos []os.FileInfo

	if count > 0 {
		if f.readdir == nil {
			f.readdir, err = f.fileInfo.readdir()
			if err != nil {
				return nil, f.pathError("Readdir", err)
			}
		}
		if len(f.readdir) >= count {
			osFileInfos = f.readdir[0:count]
			f.readdir = f.readdir[count:]
		} else {
			osFileInfos = f.readdir
			f.readdir = nil
			err = io.EOF
		}
	} else {
		osFileInfos, err = f.fileInfo.readdir()
		if err != nil {
			return nil, f.pathError("Readdir", err)
		}
	}

	return osFileInfos, err
}

func (f *fileReader) Stat() (os.FileInfo, error) {
	return f.fileInfo, nil
}

func (f *fileReader) createTempFile() error {
	if f.reader != nil {
		if err := f.reader.Close(); err != nil {
			return err
		}
		f.reader = nil
	}
	if f.file == nil {
		// Open a file that contains the contents of the zip file.
		osFile, err := createTempFile(f.fileInfo.zipFile)
		if err != nil {
			return err
		}

		f.file = osFile
	}
	return nil
}

func (f *fileReader) pathError(op string, err error) error {
	return &os.PathError{
		Op:   op,
		Path: f.name,
		Err:  err,
	}
}

// createTempFile creates a temporary file with the contents of the
// zip file. Used to implement io.Seeker interface.
func createTempFile(f *zip.File) (*os.File, error) {
	reader, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	tempFile, err := ioutil.TempFile("", "zipfs")
	if err != nil {
		return nil, err
	}

	_, err = io.Copy(tempFile, reader)
	if err != nil {
		tempFile.Close()
		os.Remove(tempFile.Name())
		return nil, err
	}
	_, err = tempFile.Seek(0, os.SEEK_SET)
	if err != nil {
		tempFile.Close()
		os.Remove(tempFile.Name())
		return nil, err
	}

	return tempFile, nil
}
