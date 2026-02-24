package containerimages

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BadgerOps/airgap/internal/config"
	"github.com/BadgerOps/airgap/internal/provider"
	"github.com/BadgerOps/airgap/internal/safety"
)

const (
	maxManifestBytes  int64 = 16 * 1024 * 1024
	maxTokenBodyBytes int64 = 1 * 1024 * 1024
	defaultOutputDir        = "images"
	defaultImageTag         = "latest"
)

var (
	manifestAcceptHeader = strings.Join([]string{
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.docker.distribution.manifest.v2+json",
		"application/vnd.docker.distribution.manifest.v1+json",
	}, ", ")
	authParamRegexp = regexp.MustCompile(`([a-zA-Z_]+)="([^"]*)"`)
	slugRegexp      = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)
)

// Provider mirrors OCI/Docker images by downloading manifests and blobs.
type Provider struct {
	name       string
	cfg        *config.ContainerImagesProviderConfig
	dataDir    string
	logger     *slog.Logger
	http       *http.Client
	tokenByKey map[string]string
}

// NewProvider creates a new container images provider.
func NewProvider(dataDir string, logger *slog.Logger) *Provider {
	if logger == nil {
		logger = slog.Default()
	}
	return &Provider{
		name:       "container_images",
		dataDir:    dataDir,
		logger:     logger,
		http:       safety.NewHTTPClient(90 * time.Second),
		tokenByKey: make(map[string]string),
	}
}

func (p *Provider) Name() string { return p.name }

func (p *Provider) SetName(name string) { p.name = name }

func (p *Provider) Type() string { return "container_images" }

func (p *Provider) Configure(rawCfg provider.ProviderConfig) error {
	cfg, err := config.ParseProviderConfig[config.ContainerImagesProviderConfig](rawCfg)
	if err != nil {
		return fmt.Errorf("parsing container images config: %w", err)
	}

	if cfg.OutputDir == "" {
		cfg.OutputDir = defaultOutputDir
	}
	if _, err := safety.CleanRelativePath(cfg.OutputDir); err != nil {
		return fmt.Errorf("invalid output_dir: %w", err)
	}

	seen := make(map[string]struct{})
	normalized := make([]string, 0, len(cfg.Images))
	for _, raw := range cfg.Images {
		ref := strings.TrimSpace(raw)
		if ref == "" {
			continue
		}
		if _, err := parseImageReference(ref); err != nil {
			return fmt.Errorf("invalid image reference %q: %w", ref, err)
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		normalized = append(normalized, ref)
	}
	cfg.Images = normalized

	p.cfg = cfg
	p.logger.Debug("configured container images provider",
		slog.Int("images", len(cfg.Images)),
		slog.String("output_dir", cfg.OutputDir),
	)
	return nil
}

func (p *Provider) Plan(ctx context.Context) (*provider.SyncPlan, error) {
	if p.cfg == nil {
		return nil, fmt.Errorf("provider not configured")
	}

	plan := &provider.SyncPlan{
		Provider:  p.Name(),
		Actions:   []provider.SyncAction{},
		Timestamp: time.Now(),
	}

	actionsByPath := make(map[string]provider.SyncAction)
	for _, raw := range p.cfg.Images {
		ref, err := parseImageReference(raw)
		if err != nil {
			p.logger.Warn("skipping invalid image reference", "reference", raw, "error", err)
			continue
		}

		imageActions, err := p.planImage(ctx, ref)
		if err != nil {
			p.logger.Error("failed to build image plan", "image", raw, "error", err)
			continue
		}
		for _, action := range imageActions {
			if _, ok := actionsByPath[action.Path]; !ok {
				actionsByPath[action.Path] = action
			}
		}
	}

	keys := make([]string, 0, len(actionsByPath))
	for path := range actionsByPath {
		keys = append(keys, path)
	}
	sort.Strings(keys)

	for _, path := range keys {
		action := actionsByPath[path]
		plan.Actions = append(plan.Actions, action)
		plan.TotalFiles++
		if action.Action == provider.ActionDownload || action.Action == provider.ActionUpdate {
			plan.TotalSize += action.Size
		}
	}

	return plan, nil
}

func (p *Provider) Sync(ctx context.Context, plan *provider.SyncPlan, opts provider.SyncOptions) (*provider.SyncReport, error) {
	report := &provider.SyncReport{
		Provider:  p.Name(),
		StartTime: time.Now(),
		Failed:    []provider.FailedFile{},
	}
	if opts.DryRun {
		report.EndTime = time.Now()
		return report, nil
	}

	for _, action := range plan.Actions {
		switch action.Action {
		case provider.ActionSkip:
			report.Skipped++
		case provider.ActionDownload, provider.ActionUpdate:
			report.Downloaded++
			report.BytesTransferred += action.Size
		case provider.ActionDelete:
			report.Deleted++
		}
	}
	report.EndTime = time.Now()
	return report, nil
}

func (p *Provider) Validate(ctx context.Context) (*provider.ValidationReport, error) {
	if p.cfg == nil {
		return nil, fmt.Errorf("provider not configured")
	}

	report := &provider.ValidationReport{
		Provider:     p.Name(),
		InvalidFiles: []provider.ValidationResult{},
		Timestamp:    time.Now(),
	}

	providerRoot, err := safety.SafeJoinUnder(p.dataDir, p.Name())
	if err != nil {
		return nil, fmt.Errorf("invalid provider root: %w", err)
	}
	validateRoot, err := safety.SafeJoinUnder(providerRoot, p.cfg.OutputDir)
	if err != nil {
		return nil, fmt.Errorf("invalid validate root: %w", err)
	}
	if _, err := os.Stat(validateRoot); os.IsNotExist(err) {
		return report, nil
	}

	err = filepath.Walk(validateRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		report.TotalFiles++
		relPath, relErr := filepath.Rel(providerRoot, path)
		if relErr != nil {
			report.InvalidFiles = append(report.InvalidFiles, provider.ValidationResult{
				Path:      path,
				LocalPath: path,
				Expected:  "",
				Actual:    "error: " + relErr.Error(),
				Valid:     false,
				Size:      info.Size(),
			})
			return nil
		}
		relPath = filepath.ToSlash(relPath)

		expectedDigest, ok := expectedDigestFromPath(relPath)
		if !ok || !strings.HasPrefix(expectedDigest, "sha256:") {
			report.ValidFiles++
			return nil
		}

		actual, hashErr := checksumLocalFile(path)
		if hashErr != nil {
			report.InvalidFiles = append(report.InvalidFiles, provider.ValidationResult{
				Path:      relPath,
				LocalPath: path,
				Expected:  strings.TrimPrefix(expectedDigest, "sha256:"),
				Actual:    "error: " + hashErr.Error(),
				Valid:     false,
				Size:      info.Size(),
			})
			return nil
		}

		expectedHash := strings.TrimPrefix(expectedDigest, "sha256:")
		if actual == expectedHash {
			report.ValidFiles++
			return nil
		}

		report.InvalidFiles = append(report.InvalidFiles, provider.ValidationResult{
			Path:      relPath,
			LocalPath: path,
			Expected:  expectedHash,
			Actual:    actual,
			Valid:     false,
			Size:      info.Size(),
		})
		return nil
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	return report, nil
}

// ImageReference is a parsed docker:// or oci:// image reference.
type ImageReference struct {
	Raw          string
	Scheme       string
	Registry     string
	EndpointHost string
	Repository   string
	Reference    string // tag or digest
	IsDigest     bool
}

type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

type imageIndexManifest struct {
	MediaType     string       `json:"mediaType"`
	SchemaVersion int          `json:"schemaVersion"`
	Manifests     []descriptor `json:"manifests"`
}

type imageManifest struct {
	MediaType     string       `json:"mediaType"`
	SchemaVersion int          `json:"schemaVersion"`
	Config        descriptor   `json:"config"`
	Layers        []descriptor `json:"layers"`
}

// ParseReference parses a docker:// or oci:// image reference.
func ParseReference(raw string) (ImageReference, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ImageReference{}, fmt.Errorf("reference is empty")
	}

	hasScheme := strings.Contains(s, "://")
	scheme := "docker"
	address := s
	if hasScheme {
		u, err := url.Parse(s)
		if err != nil {
			return ImageReference{}, fmt.Errorf("invalid URL: %w", err)
		}
		scheme = strings.ToLower(u.Scheme)
		if scheme != "docker" && scheme != "oci" {
			return ImageReference{}, fmt.Errorf("unsupported scheme %q", u.Scheme)
		}
		if u.Host == "" {
			return ImageReference{}, fmt.Errorf("registry host is required")
		}
		if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return ImageReference{}, fmt.Errorf("query, fragment, and userinfo are not supported")
		}
		address = u.Host + "/" + strings.TrimPrefix(u.Path, "/")
	}

	firstSlash := strings.Index(address, "/")
	if firstSlash <= 0 {
		if !hasScheme {
			address = "docker.io/" + address
			firstSlash = strings.Index(address, "/")
		} else {
			return ImageReference{}, fmt.Errorf("repository path is required")
		}
	}

	host := strings.ToLower(strings.TrimSpace(address[:firstSlash]))
	repoAndRef := strings.TrimSpace(address[firstSlash+1:])
	if host == "" {
		return ImageReference{}, fmt.Errorf("registry host is required")
	}
	if repoAndRef == "" {
		return ImageReference{}, fmt.Errorf("repository path is required")
	}

	if !hasScheme && !looksLikeRegistry(host) {
		repoAndRef = host + "/" + repoAndRef
		host = "docker.io"
	}

	repository := repoAndRef
	reference := defaultImageTag
	isDigest := false

	if at := strings.LastIndex(repoAndRef, "@"); at >= 0 {
		repository = repoAndRef[:at]
		reference = repoAndRef[at+1:]
		isDigest = true
	} else {
		lastSlash := strings.LastIndex(repoAndRef, "/")
		lastColon := strings.LastIndex(repoAndRef, ":")
		if lastColon > lastSlash {
			repository = repoAndRef[:lastColon]
			reference = repoAndRef[lastColon+1:]
		}
	}

	if repository == "" {
		return ImageReference{}, fmt.Errorf("repository path is required")
	}
	if strings.Contains(repository, "//") {
		return ImageReference{}, fmt.Errorf("repository path is invalid")
	}
	for _, part := range strings.Split(repository, "/") {
		if part == "" || part == "." || part == ".." {
			return ImageReference{}, fmt.Errorf("repository path is invalid")
		}
	}
	if reference == "" {
		return ImageReference{}, fmt.Errorf("reference is required")
	}
	if isDigest {
		if _, _, err := parseDigest(reference); err != nil {
			return ImageReference{}, fmt.Errorf("invalid digest reference: %w", err)
		}
	}

	if host == "docker.io" || host == "index.docker.io" {
		if !strings.Contains(repository, "/") {
			repository = "library/" + repository
		}
		host = "docker.io"
	}

	endpointHost := host
	if endpointHost == "docker.io" || endpointHost == "index.docker.io" {
		endpointHost = "registry-1.docker.io"
	}

	return ImageReference{
		Raw:          s,
		Scheme:       scheme,
		Registry:     host,
		EndpointHost: endpointHost,
		Repository:   repository,
		Reference:    reference,
		IsDigest:     isDigest,
	}, nil
}

func parseImageReference(raw string) (ImageReference, error) {
	return ParseReference(raw)
}

func looksLikeRegistry(host string) bool {
	return strings.Contains(host, ".") || strings.Contains(host, ":") || host == "localhost"
}

func parseDigest(digest string) (string, string, error) {
	parts := strings.SplitN(strings.TrimSpace(digest), ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("expected format algorithm:hex")
	}
	algo := strings.ToLower(strings.TrimSpace(parts[0]))
	hexPart := strings.ToLower(strings.TrimSpace(parts[1]))
	if algo == "" || hexPart == "" {
		return "", "", fmt.Errorf("empty digest component")
	}
	for _, c := range algo {
		if !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') && c != '.' && c != '_' && c != '+' && c != '-' {
			return "", "", fmt.Errorf("invalid digest algorithm %q", algo)
		}
	}
	for _, c := range hexPart {
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !isHex {
			return "", "", fmt.Errorf("invalid digest hex")
		}
	}
	return algo, hexPart, nil
}

func (p *Provider) planImage(ctx context.Context, ref ImageReference) ([]provider.SyncAction, error) {
	rootDesc, rootBody, authHeader, err := p.fetchManifest(ctx, ref, ref.Reference)
	if err != nil {
		return nil, err
	}
	if rootDesc.Digest == "" {
		return nil, fmt.Errorf("manifest digest missing for %s", ref.Raw)
	}

	type queueItem struct {
		desc       descriptor
		body       []byte
		authHeader string
	}

	queue := []queueItem{{desc: rootDesc, body: rootBody, authHeader: authHeader}}
	seenManifest := make(map[string]struct{})
	seenBlob := make(map[string]struct{})
	var actions []provider.SyncAction

	imageID := imagePathID(ref)

	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		if _, ok := seenManifest[item.desc.Digest]; ok {
			continue
		}
		seenManifest[item.desc.Digest] = struct{}{}

		action, err := p.newManifestAction(ref, imageID, item.desc, item.authHeader)
		if err != nil {
			return nil, err
		}
		actions = append(actions, action)

		childManifests, childBlobs, err := parseManifestDependencies(item.desc.MediaType, item.body)
		if err != nil {
			p.logger.Warn("manifest parsing failed, continuing with fetched artifact only",
				"digest", item.desc.Digest, "error", err)
			continue
		}

		for _, child := range childManifests {
			if child.Digest == "" {
				continue
			}
			if _, ok := seenManifest[child.Digest]; ok {
				continue
			}
			childDesc, childBody, childAuth, err := p.fetchManifest(ctx, ref, child.Digest)
			if err != nil {
				return nil, fmt.Errorf("fetching child manifest %s: %w", child.Digest, err)
			}
			queue = append(queue, queueItem{desc: childDesc, body: childBody, authHeader: childAuth})
		}

		for _, blob := range childBlobs {
			if blob.Digest == "" {
				continue
			}
			if _, ok := seenBlob[blob.Digest]; ok {
				continue
			}
			seenBlob[blob.Digest] = struct{}{}
			blobAction, err := p.newBlobAction(ref, imageID, blob, item.authHeader)
			if err != nil {
				return nil, err
			}
			actions = append(actions, blobAction)
		}
	}

	return actions, nil
}

func parseManifestDependencies(mediaType string, body []byte) ([]descriptor, []descriptor, error) {
	kind := manifestKind(mediaType)
	switch kind {
	case "index":
		var idx imageIndexManifest
		if err := json.Unmarshal(body, &idx); err != nil {
			return nil, nil, err
		}
		return idx.Manifests, nil, nil
	case "manifest":
		var mf imageManifest
		if err := json.Unmarshal(body, &mf); err != nil {
			return nil, nil, err
		}
		blobs := make([]descriptor, 0, len(mf.Layers)+1)
		if mf.Config.Digest != "" {
			blobs = append(blobs, mf.Config)
		}
		blobs = append(blobs, mf.Layers...)
		return nil, blobs, nil
	default:
		var idx imageIndexManifest
		if err := json.Unmarshal(body, &idx); err == nil && len(idx.Manifests) > 0 {
			return idx.Manifests, nil, nil
		}
		var mf imageManifest
		if err := json.Unmarshal(body, &mf); err == nil && (mf.Config.Digest != "" || len(mf.Layers) > 0) {
			blobs := make([]descriptor, 0, len(mf.Layers)+1)
			if mf.Config.Digest != "" {
				blobs = append(blobs, mf.Config)
			}
			blobs = append(blobs, mf.Layers...)
			return nil, blobs, nil
		}
		return nil, nil, fmt.Errorf("unrecognized manifest media type %q", mediaType)
	}
}

func manifestKind(mediaType string) string {
	mt := strings.ToLower(strings.TrimSpace(strings.Split(mediaType, ";")[0]))
	switch mt {
	case "application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json":
		return "index"
	case "application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
		"application/vnd.docker.distribution.manifest.v1+json":
		return "manifest"
	default:
		return ""
	}
}

func (p *Provider) fetchManifest(ctx context.Context, ref ImageReference, manifestRef string) (descriptor, []byte, string, error) {
	scope := fmt.Sprintf("repository:%s:pull", ref.Repository)
	manifestURL := buildManifestURL(ref, manifestRef)
	body, headers, authHeader, err := p.registryGET(ctx, manifestURL, manifestAcceptHeader, scope)
	if err != nil {
		return descriptor{}, nil, "", err
	}

	contentType := strings.TrimSpace(strings.Split(headers.Get("Content-Type"), ";")[0])
	digest := strings.TrimSpace(headers.Get("Docker-Content-Digest"))
	if digest == "" {
		sum := sha256.Sum256(body)
		digest = "sha256:" + hex.EncodeToString(sum[:])
	}
	if _, _, err := parseDigest(digest); err != nil {
		return descriptor{}, nil, "", fmt.Errorf("invalid digest %q returned by registry: %w", digest, err)
	}

	desc := descriptor{
		MediaType: contentType,
		Digest:    digest,
		Size:      int64(len(body)),
	}
	return desc, body, authHeader, nil
}

func (p *Provider) registryGET(ctx context.Context, endpoint, accept, scope string) ([]byte, http.Header, string, error) {
	tokenKey := endpoint + "|" + scope
	var authHeader string
	if token := p.tokenByKey[tokenKey]; token != "" {
		authHeader = "Bearer " + token
	}

	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, nil, "", fmt.Errorf("creating request: %w", err)
		}
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}

		resp, err := p.http.Do(req)
		if err != nil {
			return nil, nil, "", fmt.Errorf("executing request: %w", err)
		}

		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			challenge := resp.Header.Get("WWW-Authenticate")
			resp.Body.Close()
			token, err := p.fetchBearerToken(ctx, challenge, scope)
			if err != nil {
				return nil, nil, "", fmt.Errorf("fetching bearer token: %w", err)
			}
			authHeader = "Bearer " + token
			p.tokenByKey[tokenKey] = token
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			return nil, nil, "", fmt.Errorf("registry returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
		}

		data, err := safety.ReadAllWithLimit(resp.Body, maxManifestBytes)
		resp.Body.Close()
		if err != nil {
			return nil, nil, "", fmt.Errorf("reading response body: %w", err)
		}
		return data, resp.Header.Clone(), authHeader, nil
	}

	return nil, nil, "", fmt.Errorf("registry authentication failed")
}

func (p *Provider) fetchBearerToken(ctx context.Context, challenge, scope string) (string, error) {
	if challenge == "" {
		return "", fmt.Errorf("missing WWW-Authenticate challenge")
	}
	if !strings.HasPrefix(strings.ToLower(challenge), "bearer ") {
		return "", fmt.Errorf("unsupported auth challenge: %q", challenge)
	}

	params := parseAuthParams(challenge)
	realm := params["realm"]
	if realm == "" {
		return "", fmt.Errorf("bearer challenge missing realm")
	}

	values := url.Values{}
	if service := params["service"]; service != "" {
		values.Set("service", service)
	}
	tokenScope := params["scope"]
	if tokenScope == "" {
		tokenScope = scope
	}
	if tokenScope != "" {
		values.Set("scope", tokenScope)
	}

	tokenURL := realm
	if encoded := values.Encode(); encoded != "" {
		if strings.Contains(tokenURL, "?") {
			tokenURL += "&" + encoded
		} else {
			tokenURL += "?" + encoded
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating token request: %w", err)
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("executing token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("token endpoint returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	data, err := safety.ReadAllWithLimit(resp.Body, maxTokenBodyBytes)
	if err != nil {
		return "", fmt.Errorf("reading token response: %w", err)
	}
	var tokenResp struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(data, &tokenResp); err != nil {
		return "", fmt.Errorf("parsing token response: %w", err)
	}
	token := tokenResp.Token
	if token == "" {
		token = tokenResp.AccessToken
	}
	if token == "" {
		return "", fmt.Errorf("token response did not include token")
	}
	return token, nil
}

func parseAuthParams(challenge string) map[string]string {
	result := make(map[string]string)
	trimmed := strings.TrimSpace(challenge)
	if strings.HasPrefix(strings.ToLower(trimmed), "bearer ") {
		trimmed = strings.TrimSpace(trimmed[len("bearer "):])
	}
	matches := authParamRegexp.FindAllStringSubmatch(trimmed, -1)
	for _, m := range matches {
		if len(m) == 3 {
			result[strings.ToLower(m[1])] = m[2]
		}
	}
	return result
}

func (p *Provider) newManifestAction(ref ImageReference, imageID string, desc descriptor, authHeader string) (provider.SyncAction, error) {
	algo, hash, err := parseDigest(desc.Digest)
	if err != nil {
		return provider.SyncAction{}, fmt.Errorf("invalid manifest digest %q: %w", desc.Digest, err)
	}
	relPath := filepath.ToSlash(filepath.Join(
		p.cfg.OutputDir,
		imageID,
		"manifests",
		algo,
		hash+".json",
	))
	headers := make(map[string]string)
	if authHeader != "" {
		headers["Authorization"] = authHeader
	}
	headers["Accept"] = manifestAcceptHeader
	expectedChecksum := expectedChecksumForDigest(algo, hash)
	return p.buildAction(relPath, buildManifestURL(ref, desc.Digest), expectedChecksum, desc.Size, headers), nil
}

func (p *Provider) newBlobAction(ref ImageReference, imageID string, desc descriptor, authHeader string) (provider.SyncAction, error) {
	algo, hash, err := parseDigest(desc.Digest)
	if err != nil {
		return provider.SyncAction{}, fmt.Errorf("invalid blob digest %q: %w", desc.Digest, err)
	}
	relPath := filepath.ToSlash(filepath.Join(
		p.cfg.OutputDir,
		imageID,
		"blobs",
		algo,
		hash,
	))
	headers := make(map[string]string)
	if authHeader != "" {
		headers["Authorization"] = authHeader
	}
	expectedChecksum := expectedChecksumForDigest(algo, hash)
	return p.buildAction(relPath, buildBlobURL(ref, desc.Digest), expectedChecksum, desc.Size, headers), nil
}

func expectedChecksumForDigest(algo, hexPart string) string {
	if strings.EqualFold(algo, "sha256") {
		return strings.ToLower(hexPart)
	}
	return ""
}

func (p *Provider) buildAction(relPath, sourceURL, expectedChecksum string, expectedSize int64, headers map[string]string) provider.SyncAction {
	providerRoot := filepath.Join(p.dataDir, p.Name())
	localPath, err := safety.SafeJoinUnder(providerRoot, relPath)
	if err != nil {
		return provider.SyncAction{
			Path:     relPath,
			Action:   provider.ActionDownload,
			Size:     expectedSize,
			Checksum: expectedChecksum,
			URL:      sourceURL,
			Reason:   "invalid local path, redownload",
			Headers:  headers,
		}
	}

	info, statErr := os.Stat(localPath)
	if os.IsNotExist(statErr) {
		return provider.SyncAction{
			Path:     relPath,
			Action:   provider.ActionDownload,
			Size:     expectedSize,
			Checksum: expectedChecksum,
			URL:      sourceURL,
			Reason:   "new artifact",
			Headers:  headers,
		}
	}
	if statErr != nil {
		return provider.SyncAction{
			Path:     relPath,
			Action:   provider.ActionUpdate,
			Size:     expectedSize,
			Checksum: expectedChecksum,
			URL:      sourceURL,
			Reason:   "cannot stat local artifact",
			Headers:  headers,
		}
	}

	if expectedChecksum != "" {
		actual, err := checksumLocalFile(localPath)
		if err != nil {
			return provider.SyncAction{
				Path:     relPath,
				Action:   provider.ActionUpdate,
				Size:     expectedSize,
				Checksum: expectedChecksum,
				URL:      sourceURL,
				Reason:   "checksum failed",
				Headers:  headers,
			}
		}
		if actual == expectedChecksum {
			return provider.SyncAction{
				Path:     relPath,
				Action:   provider.ActionSkip,
				Size:     info.Size(),
				Checksum: expectedChecksum,
				URL:      sourceURL,
				Reason:   "checksum matches",
				Headers:  headers,
			}
		}
		return provider.SyncAction{
			Path:     relPath,
			Action:   provider.ActionUpdate,
			Size:     expectedSize,
			Checksum: expectedChecksum,
			URL:      sourceURL,
			Reason:   "checksum mismatch",
			Headers:  headers,
		}
	}

	if expectedSize > 0 && info.Size() == expectedSize {
		return provider.SyncAction{
			Path:     relPath,
			Action:   provider.ActionSkip,
			Size:     info.Size(),
			Checksum: expectedChecksum,
			URL:      sourceURL,
			Reason:   "size matches",
			Headers:  headers,
		}
	}
	if expectedSize > 0 && info.Size() != expectedSize {
		return provider.SyncAction{
			Path:     relPath,
			Action:   provider.ActionUpdate,
			Size:     expectedSize,
			Checksum: expectedChecksum,
			URL:      sourceURL,
			Reason:   "size mismatch",
			Headers:  headers,
		}
	}

	return provider.SyncAction{
		Path:     relPath,
		Action:   provider.ActionSkip,
		Size:     info.Size(),
		Checksum: expectedChecksum,
		URL:      sourceURL,
		Reason:   "file exists",
		Headers:  headers,
	}
}

func buildManifestURL(ref ImageReference, manifestRef string) string {
	return fmt.Sprintf("https://%s/v2/%s/manifests/%s",
		ref.EndpointHost,
		strings.Trim(ref.Repository, "/"),
		manifestRef,
	)
}

func buildBlobURL(ref ImageReference, digest string) string {
	return fmt.Sprintf("https://%s/v2/%s/blobs/%s",
		ref.EndpointHost,
		strings.Trim(ref.Repository, "/"),
		digest,
	)
}

// LocalImageID returns the local per-image directory slug used by this provider.
func LocalImageID(ref ImageReference) string {
	label := ref.Registry + "/" + ref.Repository
	if ref.IsDigest {
		label += "@" + ref.Reference
	} else {
		label += ":" + ref.Reference
	}
	slug := slugRegexp.ReplaceAllString(label, "_")
	slug = strings.Trim(slug, "._-")
	if slug == "" {
		return "image"
	}
	return slug
}

func imagePathID(ref ImageReference) string {
	return LocalImageID(ref)
}

func checksumLocalFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func expectedDigestFromPath(relPath string) (string, bool) {
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	for i := 0; i+2 < len(parts); i++ {
		if parts[i] != "blobs" && parts[i] != "manifests" {
			continue
		}
		algo := strings.ToLower(parts[i+1])
		hashPart := strings.TrimSuffix(strings.ToLower(parts[i+2]), ".json")
		if algo == "" || hashPart == "" {
			continue
		}
		if _, _, err := parseDigest(algo + ":" + hashPart); err != nil {
			continue
		}
		return algo + ":" + hashPart, true
	}
	return "", false
}
