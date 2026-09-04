package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var errImportPreviewStale = errors.New("import preview is stale")

const (
	previewTokenDomainImport             = "import-preview-v1"
	previewTokenDomainImportCandidate    = "import-candidate-v1"
	previewTokenDomainGameMerge          = "game-merge-preview-v1"
	previewTokenDomainHardwareAcceptance = "hardware-acceptance-preview-v1"
	previewTokenDomainManagedCleanup     = "managed-cleanup-preview-v1"
	previewTokenDomainInventoryMatch     = "inventory-match-preview-v1"
	previewTokenDomainRuntimeHintBatch   = "runtime-hint-batch-preview-v1"
	previewTokenDomainHashPack           = "hash-pack-preview-v1"
)

type importTokenContext struct {
	Kind          string `json:"kind"`
	Format        string `json:"format,omitempty"`
	Source        string `json:"source"`
	ContentRoot   string `json:"content_root,omitempty"`
	RuntimeSource string `json:"runtime_source,omitempty"`
	Platform      string `json:"platform"`
	Locale        string `json:"locale,omitempty"`
	ROMStorage    string `json:"rom_storage"`
	MediaStorage  string `json:"media_storage,omitempty"`
}

type importSnapshot struct {
	Context    importTokenContext `json:"context"`
	Candidates []importCandidate  `json:"candidates"`
}

func metadataTokenContext(in importRequest) importTokenContext {
	return importTokenContext{
		Kind:          "metadata",
		Format:        strings.ToLower(strings.TrimSpace(in.Format)),
		Source:        strings.TrimSpace(in.Source),
		ContentRoot:   strings.TrimSpace(in.ContentRoot),
		RuntimeSource: strings.TrimSpace(in.RuntimeSource),
		Platform:      strings.ToLower(strings.TrimSpace(in.Platform)),
		Locale:        strings.TrimSpace(in.Locale),
		ROMStorage:    defaultValue(in.ROMStorage, "reference"),
		MediaStorage:  defaultValue(in.MediaStorage, "copy"),
	}
}

func romTokenContext(in romImportRequest) importTokenContext {
	return importTokenContext{
		Kind:         "rom-scan",
		Source:       strings.TrimSpace(in.Source),
		Platform:     strings.ToLower(strings.TrimSpace(in.Platform)),
		ROMStorage:   defaultValue(in.ROMStorage, "reference"),
		MediaStorage: "ignore",
	}
}

func (s *Server) signPreviewValue(domain string, value any) (string, error) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return "", errors.New("preview token domain is required")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, s.importKey[:])
	if _, err = mac.Write([]byte("varkiv-preview-token-v1\x00")); err != nil {
		return "", err
	}
	if _, err = mac.Write([]byte(domain)); err != nil {
		return "", err
	}
	if _, err = mac.Write([]byte{0}); err != nil {
		return "", err
	}
	if _, err = mac.Write(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *Server) sealImportPreview(context importTokenContext, candidates []importCandidate) (string, []importCandidate, error) {
	unsigned := make([]importCandidate, len(candidates))
	copy(unsigned, candidates)
	for index := range unsigned {
		unsigned[index].Token = ""
	}
	previewToken, err := s.signPreviewValue(previewTokenDomainImport, importSnapshot{Context: context, Candidates: unsigned})
	if err != nil {
		return "", nil, err
	}
	sealed := make([]importCandidate, len(unsigned))
	copy(sealed, unsigned)
	for index := range sealed {
		sealed[index].Token, err = s.signPreviewValue(previewTokenDomainImportCandidate, struct {
			PreviewToken string `json:"preview_token"`
			Index        int    `json:"index"`
		}{PreviewToken: previewToken, Index: index})
		if err != nil {
			return "", nil, err
		}
	}
	return previewToken, sealed, nil
}

func verifyImportSelection(expectedPreview, suppliedPreview string, candidates []importCandidate, selectedTokens []string) (map[int]bool, error) {
	if suppliedPreview == "" || !hmac.Equal([]byte(expectedPreview), []byte(suppliedPreview)) {
		return nil, errImportPreviewStale
	}
	if len(selectedTokens) == 0 {
		return nil, errors.New("selected_tokens must contain at least one candidate token")
	}
	available := make(map[string]int, len(candidates))
	for index, candidate := range candidates {
		available[candidate.Token] = index
	}
	selected := make(map[int]bool, len(selectedTokens))
	for _, token := range selectedTokens {
		index, ok := available[token]
		if !ok {
			return nil, fmt.Errorf("%w: selected candidate token no longer matches this preview", errImportPreviewStale)
		}
		if selected[index] {
			return nil, errors.New("selected_tokens must not contain duplicates")
		}
		selected[index] = true
	}
	return selected, nil
}
