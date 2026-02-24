package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BadgerOps/airgap/internal/config"
	"github.com/BadgerOps/airgap/internal/provider/containerimages"
	"github.com/BadgerOps/airgap/internal/safety"
)

// RegistryPushOptions configures pushing mirrored container images to a target registry.
type RegistryPushOptions struct {
	SourceProvider string
	TargetProvider string
	DryRun         bool
}

// RegistryPushReport summarizes an image push operation.
type RegistryPushReport struct {
	SourceProvider  string
	TargetProvider  string
	ImagesTotal     int
	ImagesPushed    int
	BlobsProcessed  int
	ManifestsPushed int
	Failures        []string
	Duration        time.Duration
}

type localManifest struct {
	Digest          string
	MediaType       string
	Bytes           []byte
	ChildManifests  []string
	ReferencedBlobs []string
}

type localImageBundle struct {
	ImageRoot      string
	SourceRef      containerimages.ImageReference
	RootDigest     string
	RootMediaType  string
	Manifests      map[string]*localManifest
	ManifestOrder  []string
	RequiredBlobs  []string
	BlobSourcePath map[string]string
}

// PushContainerImages pushes mirrored container images from a container_images provider
// to a configured registry target.
func (m *SyncManager) PushContainerImages(ctx context.Context, opts RegistryPushOptions) (*RegistryPushReport, error) {
	start := time.Now()
	if opts.SourceProvider == "" {
		return nil, fmt.Errorf("source provider is required")
	}
	if opts.TargetProvider == "" {
		return nil, fmt.Errorf("target provider is required")
	}
	if m.store == nil {
		return nil, fmt.Errorf("store not initialized")
	}

	sourcePC, err := m.store.GetProviderConfig(opts.SourceProvider)
	if err != nil {
		return nil, fmt.Errorf("reading source provider config %q: %w", opts.SourceProvider, err)
	}
	if sourcePC.Type != "container_images" {
		return nil, fmt.Errorf("provider %q is type %q, expected container_images", opts.SourceProvider, sourcePC.Type)
	}

	targetPC, err := m.store.GetProviderConfig(opts.TargetProvider)
	if err != nil {
		return nil, fmt.Errorf("reading target provider config %q: %w", opts.TargetProvider, err)
	}
	if targetPC.Type != "registry" {
		return nil, fmt.Errorf("provider %q is type %q, expected registry", opts.TargetProvider, targetPC.Type)
	}

	sourceCfg, err := parseProviderConfigJSON[config.ContainerImagesProviderConfig](sourcePC.ConfigJSON)
	if err != nil {
		return nil, fmt.Errorf("parsing container images config: %w", err)
	}
	if sourceCfg.OutputDir == "" {
		sourceCfg.OutputDir = "images"
	}

	targetCfg, err := parseProviderConfigJSON[config.RegistryProviderConfig](targetPC.ConfigJSON)
	if err != nil {
		return nil, fmt.Errorf("parsing registry config: %w", err)
	}
	if targetCfg.Endpoint == "" {
		return nil, fmt.Errorf("registry endpoint is required on provider %q", opts.TargetProvider)
	}
	if targetCfg.SkopeoBinary == "" {
		targetCfg.SkopeoBinary = "skopeo"
	}

	if _, err := exec.LookPath(targetCfg.SkopeoBinary); err != nil {
		return nil, fmt.Errorf("skopeo binary %q not found in PATH: %w", targetCfg.SkopeoBinary, err)
	}

	report := &RegistryPushReport{
		SourceProvider: opts.SourceProvider,
		TargetProvider: opts.TargetProvider,
		ImagesTotal:    len(sourceCfg.Images),
	}

	if len(sourceCfg.Images) == 0 {
		report.Duration = time.Since(start)
		return report, nil
	}

	sourceRoot, err := safety.SafeJoinUnder(m.config.Server.DataDir, opts.SourceProvider)
	if err != nil {
		return nil, fmt.Errorf("invalid source provider root: %w", err)
	}

	for _, raw := range sourceCfg.Images {
		select {
		case <-ctx.Done():
			report.Duration = time.Since(start)
			return report, ctx.Err()
		default:
		}

		ref, err := containerimages.ParseReference(raw)
		if err != nil {
			report.Failures = append(report.Failures, fmt.Sprintf("%s: invalid reference: %v", raw, err))
			continue
		}

		imageDirRel := filepath.Join(sourceCfg.OutputDir, containerimages.LocalImageID(ref))
		imageRoot, err := safety.SafeJoinUnder(sourceRoot, imageDirRel)
		if err != nil {
			report.Failures = append(report.Failures, fmt.Sprintf("%s: invalid local image path: %v", raw, err))
			continue
		}

		bundle, err := loadLocalImageBundle(imageRoot, ref)
		if err != nil {
			report.Failures = append(report.Failures, fmt.Sprintf("%s: load failed: %v", raw, err))
			continue
		}

		destRepo := buildDestinationRepository(ref.Repository, targetCfg.RepositoryPrefix)
		stats, err := pushImageBundleWithSkopeo(ctx, bundle, targetCfg, destRepo, opts.DryRun, m.logger)
		if err != nil {
			report.Failures = append(report.Failures, fmt.Sprintf("%s -> %s/%s: %v", raw, targetCfg.Endpoint, destRepo, err))
			continue
		}

		report.ImagesPushed++
		report.BlobsProcessed += stats.Blobs
		report.ManifestsPushed += stats.Manifests
	}

	report.Duration = time.Since(start)
	if len(report.Failures) > 0 {
		return report, fmt.Errorf("failed to push %d image(s)", len(report.Failures))
	}
	return report, nil
}

func parseProviderConfigJSON[T any](cfgJSON string) (*T, error) {
	var raw map[string]interface{}
	if strings.TrimSpace(cfgJSON) != "" {
		if err := json.Unmarshal([]byte(cfgJSON), &raw); err != nil {
			return nil, err
		}
	}
	if raw == nil {
		raw = map[string]interface{}{}
	}
	return config.ParseProviderConfig[T](raw)
}

func buildDestinationRepository(sourceRepo, prefix string) string {
	sourceRepo = strings.Trim(strings.TrimSpace(sourceRepo), "/")
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return sourceRepo
	}
	return prefix + "/" + sourceRepo
}

func loadLocalImageBundle(imageRoot string, ref containerimages.ImageReference) (*localImageBundle, error) {
	manifestFiles, err := filepath.Glob(filepath.Join(imageRoot, "manifests", "*", "*.json"))
	if err != nil {
		return nil, fmt.Errorf("listing manifest files: %w", err)
	}
	if len(manifestFiles) == 0 {
		return nil, fmt.Errorf("no manifest files found under %s", imageRoot)
	}

	manifests := make(map[string]*localManifest, len(manifestFiles))
	for _, p := range manifestFiles {
		algo := filepath.Base(filepath.Dir(p))
		hash := strings.TrimSuffix(filepath.Base(p), ".json")
		digest := algo + ":" + hash

		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("reading manifest %s: %w", p, err)
		}

		mediaType, childDigests, blobDigests := parseManifestDetails(data)
		manifests[digest] = &localManifest{
			Digest:          digest,
			MediaType:       mediaType,
			Bytes:           data,
			ChildManifests:  childDigests,
			ReferencedBlobs: blobDigests,
		}
	}

	rootDigest, err := chooseRootDigest(ref, manifests)
	if err != nil {
		return nil, err
	}
	rootManifest := manifests[rootDigest]
	if rootManifest == nil {
		return nil, fmt.Errorf("root manifest %s not found", rootDigest)
	}

	manifestOrder := buildManifestPostOrder(rootDigest, manifests)
	requiredBlobs := collectRequiredBlobDigests(manifestOrder, manifests)
	blobPaths := mapLocalBlobPaths(filepath.Join(imageRoot, "blobs"))

	for _, d := range requiredBlobs {
		if _, ok := blobPaths[d]; !ok {
			return nil, fmt.Errorf("required blob %s not found in local cache", d)
		}
	}

	return &localImageBundle{
		ImageRoot:      imageRoot,
		SourceRef:      ref,
		RootDigest:     rootDigest,
		RootMediaType:  rootManifest.MediaType,
		Manifests:      manifests,
		ManifestOrder:  manifestOrder,
		RequiredBlobs:  requiredBlobs,
		BlobSourcePath: blobPaths,
	}, nil
}

func parseManifestDetails(data []byte) (mediaType string, childDigests []string, blobDigests []string) {
	var probe struct {
		MediaType string `json:"mediaType"`
		Manifests []struct {
			Digest string `json:"digest"`
		} `json:"manifests"`
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
		Layers []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return "", nil, nil
	}

	mediaType = strings.TrimSpace(probe.MediaType)
	for _, m := range probe.Manifests {
		d := strings.TrimSpace(m.Digest)
		if d != "" {
			childDigests = append(childDigests, d)
		}
	}

	if d := strings.TrimSpace(probe.Config.Digest); d != "" {
		blobDigests = append(blobDigests, d)
	}
	for _, l := range probe.Layers {
		d := strings.TrimSpace(l.Digest)
		if d != "" {
			blobDigests = append(blobDigests, d)
		}
	}

	if mediaType == "" {
		if len(childDigests) > 0 {
			mediaType = "application/vnd.oci.image.index.v1+json"
		} else if len(blobDigests) > 0 {
			mediaType = "application/vnd.oci.image.manifest.v1+json"
		}
	}
	return mediaType, uniqueStrings(childDigests), uniqueStrings(blobDigests)
}

func chooseRootDigest(ref containerimages.ImageReference, manifests map[string]*localManifest) (string, error) {
	if ref.IsDigest {
		if _, ok := manifests[ref.Reference]; ok {
			return ref.Reference, nil
		}
	}

	inbound := make(map[string]int, len(manifests))
	for d := range manifests {
		inbound[d] = 0
	}
	for _, m := range manifests {
		for _, child := range m.ChildManifests {
			if _, ok := inbound[child]; ok {
				inbound[child]++
			}
		}
	}

	var candidates []string
	for d, n := range inbound {
		if n == 0 {
			candidates = append(candidates, d)
		}
	}
	sort.Strings(candidates)

	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("unable to determine root manifest (no root candidates)")
	}

	var indexCandidates []string
	for _, d := range candidates {
		mt := strings.ToLower(manifests[d].MediaType)
		if strings.Contains(mt, "index") || strings.Contains(mt, "manifest.list") {
			indexCandidates = append(indexCandidates, d)
		}
	}
	if len(indexCandidates) == 1 {
		return indexCandidates[0], nil
	}

	return "", fmt.Errorf("unable to determine root manifest (multiple candidates: %s)", strings.Join(candidates, ", "))
}

func buildManifestPostOrder(root string, manifests map[string]*localManifest) []string {
	seen := make(map[string]bool, len(manifests))
	order := make([]string, 0, len(manifests))
	var visit func(string)
	visit = func(digest string) {
		if seen[digest] {
			return
		}
		seen[digest] = true
		m := manifests[digest]
		if m == nil {
			return
		}
		for _, child := range m.ChildManifests {
			if _, ok := manifests[child]; ok {
				visit(child)
			}
		}
		order = append(order, digest)
	}
	visit(root)
	return order
}

func collectRequiredBlobDigests(manifestOrder []string, manifests map[string]*localManifest) []string {
	seen := make(map[string]bool)
	var out []string
	for _, d := range manifestOrder {
		m := manifests[d]
		if m == nil {
			continue
		}
		for _, b := range m.ReferencedBlobs {
			if b == "" || seen[b] {
				continue
			}
			seen[b] = true
			out = append(out, b)
		}
	}
	sort.Strings(out)
	return out
}

func mapLocalBlobPaths(blobsRoot string) map[string]string {
	paths := map[string]string{}
	files, _ := filepath.Glob(filepath.Join(blobsRoot, "*", "*"))
	for _, p := range files {
		algo := filepath.Base(filepath.Dir(p))
		hash := filepath.Base(p)
		if algo == "" || hash == "" {
			continue
		}
		paths[algo+":"+hash] = p
	}
	return paths
}

type pushStats struct {
	Blobs     int
	Manifests int
}

func pushImageBundleWithSkopeo(
	ctx context.Context,
	bundle *localImageBundle,
	targetCfg *config.RegistryProviderConfig,
	destRepo string,
	dryRun bool,
	logger *slog.Logger,
) (*pushStats, error) {
	stats := &pushStats{
		Blobs:     len(bundle.RequiredBlobs),
		Manifests: len(bundle.ManifestOrder),
	}

	layoutRefName := bundle.SourceRef.Reference
	if bundle.SourceRef.IsDigest {
		layoutRefName = digestToTagAlias(bundle.SourceRef.Reference)
	}
	if layoutRefName == "" {
		layoutRefName = "latest"
	}

	if dryRun {
		return stats, nil
	}

	layoutDir, err := os.MkdirTemp("", "airgap-oci-layout-*")
	if err != nil {
		return nil, fmt.Errorf("creating temporary OCI layout dir: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(layoutDir)
	}()

	if err := writeOCILayout(layoutDir, bundle, layoutRefName); err != nil {
		return nil, err
	}

	endpoint := normalizeRegistryEndpoint(targetCfg.Endpoint)
	dest := fmt.Sprintf("docker://%s/%s", endpoint, strings.Trim(destRepo, "/"))
	if bundle.SourceRef.IsDigest {
		dest += "@" + bundle.SourceRef.Reference
	} else {
		dest += ":" + bundle.SourceRef.Reference
	}

	src := fmt.Sprintf("oci:%s:%s", layoutDir, layoutRefName)
	args := []string{"copy", "--all"}
	if targetCfg.InsecureSkipTLS {
		args = append(args, "--dest-tls-verify=false")
	}
	if targetCfg.Username != "" || targetCfg.Password != "" {
		args = append(args, "--dest-creds", targetCfg.Username+":"+targetCfg.Password)
	}
	args = append(args, src, dest)

	cmd := exec.CommandContext(ctx, targetCfg.SkopeoBinary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if logger != nil {
			logger.Error("skopeo copy failed",
				"source", src,
				"destination", dest,
				"error", err,
				"output", string(out),
			)
		}
		return nil, fmt.Errorf("skopeo copy failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if logger != nil {
		logger.Info("image pushed to registry", "destination", dest)
	}
	return stats, nil
}

func normalizeRegistryEndpoint(endpoint string) string {
	e := strings.TrimSpace(endpoint)
	e = strings.TrimPrefix(e, "https://")
	e = strings.TrimPrefix(e, "http://")
	return strings.TrimRight(e, "/")
}

func digestToTagAlias(d string) string {
	repl := strings.NewReplacer(":", "-", "/", "-", "@", "-")
	return "digest-" + repl.Replace(strings.ToLower(d))
}

func writeOCILayout(layoutDir string, bundle *localImageBundle, refName string) error {
	blobsRoot := filepath.Join(layoutDir, "blobs")
	if err := os.MkdirAll(blobsRoot, 0o755); err != nil {
		return fmt.Errorf("creating blobs root: %w", err)
	}

	for _, digest := range bundle.ManifestOrder {
		algo, hash, ok := splitDigest(digest)
		if !ok {
			return fmt.Errorf("invalid manifest digest: %s", digest)
		}
		dest := filepath.Join(blobsRoot, algo, hash)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("creating manifest digest dir: %w", err)
		}
		if err := os.WriteFile(dest, bundle.Manifests[digest].Bytes, 0o644); err != nil {
			return fmt.Errorf("writing manifest blob %s: %w", digest, err)
		}
	}

	for _, digest := range bundle.RequiredBlobs {
		src := bundle.BlobSourcePath[digest]
		if src == "" {
			return fmt.Errorf("missing source path for blob %s", digest)
		}
		algo, hash, ok := splitDigest(digest)
		if !ok {
			return fmt.Errorf("invalid blob digest: %s", digest)
		}
		dest := filepath.Join(blobsRoot, algo, hash)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("creating blob digest dir: %w", err)
		}
		if err := linkOrCopyFile(src, dest); err != nil {
			return fmt.Errorf("copying blob %s: %w", digest, err)
		}
	}

	if err := os.WriteFile(filepath.Join(layoutDir, "oci-layout"), []byte(`{"imageLayoutVersion":"1.0.0"}`), 0o644); err != nil {
		return fmt.Errorf("writing oci-layout file: %w", err)
	}

	rootManifest := bundle.Manifests[bundle.RootDigest]
	if rootManifest == nil {
		return fmt.Errorf("root manifest %s not found", bundle.RootDigest)
	}

	index := map[string]interface{}{
		"schemaVersion": 2,
		"manifests": []map[string]interface{}{
			{
				"mediaType": rootManifest.MediaType,
				"digest":    bundle.RootDigest,
				"size":      len(rootManifest.Bytes),
				"annotations": map[string]string{
					"org.opencontainers.image.ref.name": refName,
				},
			},
		},
	}
	indexBytes, err := json.Marshal(index)
	if err != nil {
		return fmt.Errorf("marshaling index.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(layoutDir, "index.json"), indexBytes, 0o644); err != nil {
		return fmt.Errorf("writing index.json: %w", err)
	}

	return nil
}

func splitDigest(d string) (string, string, bool) {
	parts := strings.SplitN(strings.TrimSpace(d), ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	algo := strings.TrimSpace(parts[0])
	hash := strings.TrimSpace(parts[1])
	if algo == "" || hash == "" {
		return "", "", false
	}
	return algo, hash, true
}

func linkOrCopyFile(src, dst string) error {
	if err := os.Link(src, dst); err == nil {
		return nil
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		_ = in.Close()
	}()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
