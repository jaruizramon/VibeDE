package server

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	maxSearchResults    = 300
	maxSearchFiles      = 20000
	maxSearchFileBytes  = 2 << 20
	maxSearchTotalBytes = 200 << 20
)

type codeMatch struct {
	Path        string `json:"path"`
	Line        int    `json:"line"` // 1-based
	Column      int    `json:"column"`
	MatchLen    int    `json:"matchLen"`
	Occurrences int    `json:"occurrences"`
	Preview     string `json:"preview"`
	MatchStart  int    `json:"matchStart"` // byte offset within Preview
}

type codeSearchResponse struct {
	Query         string      `json:"query"`
	CaseSensitive bool        `json:"caseSensitive"`
	Matches       []codeMatch `json:"matches"`
	Files         int         `json:"files"`
	Scanned       int         `json:"scanned"`
	Truncated     bool        `json:"truncated"`
	TookMS        int64       `json:"tookMs"`
}

func (s *Server) handleCodeSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	caseSensitive := r.URL.Query().Get("case") == "1"
	if query == "" {
		writeJSON(w, http.StatusOK, codeSearchResponse{
			Query: query, CaseSensitive: caseSensitive, Matches: []codeMatch{},
		})
		return
	}
	resp, err := s.searchCode(r.Context(), query, caseSensitive)
	if err != nil {
		s.writeFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) searchCode(ctx context.Context, query string, caseSensitive bool) (codeSearchResponse, error) {
	started := time.Now()
	out := codeSearchResponse{
		Query:         query,
		CaseSensitive: caseSensitive,
		Matches:       []codeMatch{},
	}
	needle := query
	if !caseSensitive {
		needle = strings.ToLower(needle)
	}
	filesWithMatch := make(map[string]bool)
	var totalBytes int64
	stop := false
	err := s.ws.WalkFiles(func(rel string, size int64) bool {
		if ctx.Err() != nil {
			stop = true
			return false
		}
		if len(out.Matches) >= maxSearchResults || out.Scanned >= maxSearchFiles {
			out.Truncated = true
			return false
		}
		if size <= 0 || size > maxSearchFileBytes {
			return true
		}
		if totalBytes+size > maxSearchTotalBytes {
			out.Truncated = true
			return false
		}
		body, err := s.ws.Read(rel)
		if err != nil {
			return true
		}
		totalBytes += int64(len(body))
		out.Scanned++
		lines := strings.Split(string(body), "\n")
		for i, line := range lines {
			line = strings.TrimSuffix(line, "\r")
			hay := line
			if !caseSensitive {
				hay = strings.ToLower(line)
			}
			idx := strings.Index(hay, needle)
			if idx < 0 {
				continue
			}
			occurrences := strings.Count(hay, needle)
			previewStart := idx - 80
			if previewStart < 0 {
				previewStart = 0
			}
			previewEnd := idx + len(needle) + 120
			if previewEnd > len(line) {
				previewEnd = len(line)
			}
			prefix, suffix := "", ""
			if previewStart > 0 {
				prefix = "…"
			}
			if previewEnd < len(line) {
				suffix = "…"
			}
			preview := prefix + line[previewStart:previewEnd] + suffix
			out.Matches = append(out.Matches, codeMatch{
				Path:        rel,
				Line:        i + 1,
				Column:      idx,
				MatchLen:    len(query),
				Occurrences: occurrences,
				Preview:     preview,
				MatchStart:  len(prefix) + idx - previewStart,
			})
			filesWithMatch[rel] = true
			if len(out.Matches) >= maxSearchResults {
				out.Truncated = true
				return false
			}
		}
		return true
	})
	if err != nil && ctx.Err() == nil {
		return out, err
	}
	if stop && ctx.Err() != nil {
		return out, ctx.Err()
	}
	sort.SliceStable(out.Matches, func(i, j int) bool {
		a, b := out.Matches[i], out.Matches[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
	out.Files = len(filesWithMatch)
	out.TookMS = time.Since(started).Milliseconds()
	return out, nil
}
