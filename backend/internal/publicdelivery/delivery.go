package publicdelivery

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	pathpkg "path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	manifestPath     = "assets/manifest.json"
	manifestSchema   = "ascendany.public-assets.v1"
	maximumFiles     = 256
	maximumAssetSize = 4 << 20
	maximumTotalSize = 16 << 20
)

const (
	staticContentSecurityPolicy = "default-src 'self'; base-uri 'self'; connect-src 'self'; font-src 'self'; form-action 'self'; frame-ancestors 'none'; img-src 'self' data:; manifest-src 'self'; media-src 'none'; object-src 'none'; script-src 'self'; style-src 'self'; style-src-attr 'unsafe-inline'; worker-src 'none'"
	siteContentSecurityPolicy   = "default-src 'self'; base-uri 'self'; connect-src 'self' https://api.github.com; font-src 'self'; form-action 'self'; frame-ancestors 'none'; img-src 'self' data:; manifest-src 'self'; media-src 'none'; object-src 'none'; script-src 'self'; style-src 'self'; style-src-attr 'unsafe-inline'; worker-src 'none'"
)

var (
	immutableAssetName = regexp.MustCompile(`-[0-9A-Za-z_-]{8,}\.[0-9A-Za-z]+$`)
	canonicalAssetPath = regexp.MustCompile(`^[0-9A-Za-z._/-]+$`)
	httpQualityValue   = regexp.MustCompile(`^(?:0(?:\.[0-9]{0,3})?|1(?:\.0{0,3})?)$`)
)

//go:embed assets
var embeddedAssets embed.FS

type manifest struct {
	Schema string         `json:"schema"`
	Routes manifestRoutes `json:"routes"`
	Files  []manifestFile `json:"files"`
}

type manifestRoutes struct {
	Site          string `json:"site"`
	StudentWeb    string `json:"studentWeb"`
	ImportConsole string `json:"importConsole"`
}

type manifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Cache  string `json:"cache"`
}

type asset struct {
	body        []byte
	contentType string
	cache       string
	etag        string
}

type Handler struct {
	api    http.Handler
	assets map[string]asset
}

func New(api http.Handler) (*Handler, error) {
	return newHandler(api, embeddedAssets)
}

func newHandler(api http.Handler, source fs.FS) (*Handler, error) {
	if api == nil {
		return nil, errors.New("public delivery requires the Go API handler")
	}
	assets, err := loadAssets(source)
	if err != nil {
		return nil, fmt.Errorf("load embedded public assets: %w", err)
	}
	return &Handler{api: api, assets: assets}, nil
}

func loadAssets(source fs.FS) (map[string]asset, error) {
	document, err := readBoundedFile(source, manifestPath, 64<<10)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var declared manifest
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&declared); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if err := requireJSONEnd(decoder); err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}
	if declared.Schema != manifestSchema || declared.Routes != (manifestRoutes{
		Site: "/", StudentWeb: "/app/", ImportConsole: "/admin/",
	}) {
		return nil, errors.New("manifest schema or route ownership differs from the compiled contract")
	}
	if len(declared.Files) == 0 || len(declared.Files) > maximumFiles {
		return nil, errors.New("manifest file count exceeds the compiled contract")
	}

	embeddedPaths, err := regularAssetPaths(source)
	if err != nil {
		return nil, err
	}
	if len(embeddedPaths) != len(declared.Files) {
		return nil, errors.New("manifest does not enumerate the exact embedded asset set")
	}

	loaded := make(map[string]asset, len(declared.Files))
	var previous string
	var total int64
	for index, entry := range declared.Files {
		if err := validateManifestEntry(entry); err != nil {
			return nil, fmt.Errorf("manifest file %d: %w", index, err)
		}
		if index > 0 && entry.Path <= previous {
			return nil, errors.New("manifest paths must be unique and byte-sorted")
		}
		if embeddedPaths[index] != entry.Path {
			return nil, errors.New("manifest path order differs from the exact embedded asset set")
		}
		previous = entry.Path
		body, err := readBoundedFile(source, "assets/"+entry.Path, maximumAssetSize)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", entry.Path, err)
		}
		if int64(len(body)) != entry.Size {
			return nil, fmt.Errorf("asset size differs from manifest: %s", entry.Path)
		}
		digest := sha256.Sum256(body)
		if hex.EncodeToString(digest[:]) != entry.SHA256 {
			return nil, fmt.Errorf("asset digest differs from manifest: %s", entry.Path)
		}
		total += int64(len(body))
		if total > maximumTotalSize {
			return nil, errors.New("embedded public assets exceed the compiled byte limit")
		}
		contentType, ok := contentTypeForPath(entry.Path)
		if !ok {
			return nil, fmt.Errorf("asset content type is unsupported: %s", entry.Path)
		}
		loaded[entry.Path] = asset{
			body:        body,
			contentType: contentType,
			cache:       entry.Cache,
			etag:        `"` + entry.SHA256 + `"`,
		}
	}
	for _, required := range []string{"site/index.html", "app/index.html", "admin/index.html"} {
		if _, exists := loaded[required]; !exists {
			return nil, fmt.Errorf("required public entrypoint is missing: %s", required)
		}
	}
	return loaded, nil
}

func requireJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("manifest contains multiple JSON values")
		}
		return err
	}
	return nil
}

func readBoundedFile(source fs.FS, name string, maximum int64) ([]byte, error) {
	file, err := source.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maximum {
		return nil, errors.New("file exceeds the compiled byte limit")
	}
	return body, nil
}

func regularAssetPaths(source fs.FS) ([]string, error) {
	paths := make([]string, 0, maximumFiles)
	err := fs.WalkDir(source, "assets", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("embedded asset tree contains a symbolic link: %s", name)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("embedded asset tree contains a non-regular file: %s", name)
		}
		if name == manifestPath {
			return nil
		}
		paths = append(paths, strings.TrimPrefix(name, "assets/"))
		if len(paths) > maximumFiles {
			return errors.New("embedded asset file count exceeds the compiled contract")
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("enumerate embedded assets: %w", err)
	}
	return paths, nil
}

func validateManifestEntry(entry manifestFile) error {
	if !fs.ValidPath(entry.Path) || !canonicalAssetPath.MatchString(entry.Path) || strings.Contains(entry.Path, `\`) || len(entry.Path) > 512 ||
		(!strings.HasPrefix(entry.Path, "site/") && !strings.HasPrefix(entry.Path, "app/") && !strings.HasPrefix(entry.Path, "admin/")) {
		return errors.New("asset path is outside the compiled ownership roots")
	}
	if len(entry.SHA256) != sha256.Size*2 {
		return errors.New("asset digest is not canonical SHA-256")
	}
	if _, err := hex.DecodeString(entry.SHA256); err != nil || strings.ToLower(entry.SHA256) != entry.SHA256 {
		return errors.New("asset digest is not canonical SHA-256")
	}
	if entry.Size < 0 || entry.Size > maximumAssetSize {
		return errors.New("asset size exceeds the compiled contract")
	}
	wantCache := "revalidate"
	if strings.Contains(entry.Path, "/assets/") {
		if !immutableAssetName.MatchString(pathpkg.Base(entry.Path)) {
			return errors.New("immutable asset name lacks a content hash")
		}
		wantCache = "immutable"
	}
	if entry.Cache != wantCache {
		return errors.New("asset cache class differs from its path contract")
	}
	return nil
}

func contentTypeForPath(name string) (string, bool) {
	suffixes := map[string]string{
		".css":   "text/css; charset=utf-8",
		".html":  "text/html; charset=utf-8",
		".ico":   "image/x-icon",
		".js":    "text/javascript; charset=utf-8",
		".json":  "application/json; charset=utf-8",
		".png":   "image/png",
		".svg":   "image/svg+xml",
		".webp":  "image/webp",
		".woff":  "font/woff",
		".woff2": "font/woff2",
	}
	contentType, ok := suffixes[pathpkg.Ext(name)]
	return contentType, ok
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if apiPath(request.URL.Path) {
		handler.api.ServeHTTP(writer, request)
		return
	}
	setStaticSecurityHeaders(writer.Header())
	if !canonicalStaticPath(request) {
		writeStaticError(writer, http.StatusBadRequest, "Invalid public asset path.")
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		writeStaticError(writer, http.StatusMethodNotAllowed, "Method is not allowed for public assets.")
		return
	}
	if request.URL.Path == "/app" {
		writer.Header().Set("Cache-Control", "no-cache")
		http.Redirect(writer, request, "/app/", http.StatusPermanentRedirect)
		return
	}
	if request.URL.Path == "/admin" {
		writer.Header().Set("Cache-Control", "no-cache")
		http.Redirect(writer, request, "/admin/", http.StatusPermanentRedirect)
		return
	}

	name, fallback := assetName(request.URL.Path)
	value, exists := handler.assets[name]
	if !exists && fallback != "" && acceptsHTML(request.Header.Get("Accept")) && pathpkg.Ext(strings.TrimSuffix(request.URL.Path, "/")) == "" {
		value, exists = handler.assets[fallback]
		name = fallback
		if exists {
			writer.Header().Set("Vary", "Accept")
		}
	}
	if !exists {
		writeStaticError(writer, http.StatusNotFound, "Public asset does not exist.")
		return
	}
	serveAsset(writer, request, name, value)
}

func apiPath(name string) bool {
	return name == "/api/v2" || strings.HasPrefix(name, "/api/v2/") ||
		name == "/livez" || name == "/readyz" || name == "/version"
}

func canonicalStaticPath(request *http.Request) bool {
	name := request.URL.Path
	if name == "" || name[0] != '/' || request.URL.RawPath != "" || strings.ContainsAny(name, "\\\x00") {
		return false
	}
	cleaned := pathpkg.Clean(name)
	return name == cleaned || name == cleaned+"/"
}

func assetName(publicPath string) (string, string) {
	switch {
	case publicPath == "/":
		return "site/index.html", ""
	case strings.HasPrefix(publicPath, "/app/"):
		remainder := strings.TrimPrefix(publicPath, "/app/")
		if remainder == "" {
			return "app/index.html", ""
		}
		return "app/" + remainder, "app/index.html"
	case strings.HasPrefix(publicPath, "/admin/"):
		remainder := strings.TrimPrefix(publicPath, "/admin/")
		if remainder == "" {
			return "admin/index.html", ""
		}
		return "admin/" + remainder, "admin/index.html"
	default:
		return "site/" + strings.TrimPrefix(publicPath, "/"), ""
	}
}

func acceptsHTML(value string) bool {
	for _, item := range strings.Split(value, ",") {
		mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(item))
		if err != nil || !strings.EqualFold(mediaType, "text/html") {
			continue
		}
		quality := 1.0
		if encodedQuality, exists := parameters["q"]; exists {
			if !httpQualityValue.MatchString(encodedQuality) {
				continue
			}
			quality, err = strconv.ParseFloat(encodedQuality, 64)
			if err != nil {
				continue
			}
		}
		if quality > 0 {
			return true
		}
	}
	return false
}

func setStaticSecurityHeaders(header http.Header) {
	header.Set("Content-Security-Policy", staticContentSecurityPolicy)
	header.Set("Cross-Origin-Opener-Policy", "same-origin")
	header.Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
}

func writeStaticError(writer http.ResponseWriter, status int, message string) {
	writer.Header().Set("Cache-Control", "no-store")
	http.Error(writer, message, status)
}

func serveAsset(writer http.ResponseWriter, request *http.Request, name string, value asset) {
	if strings.HasPrefix(name, "site/") {
		writer.Header().Set("Content-Security-Policy", siteContentSecurityPolicy)
	}
	writer.Header().Set("Content-Type", value.contentType)
	writer.Header().Set("ETag", value.etag)
	if value.cache == "immutable" {
		writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		writer.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(writer, request, pathpkg.Base(name), time.Time{}, bytes.NewReader(value.body))
}
