//go:build js && wasm

package main

import (
	"fmt"
	"strconv"
	"syscall/js"
	"time"
)

func (a *app) focusCodeSearch() {
	a.mu.Lock()
	input := a.els.codeSearchInput
	a.mu.Unlock()
	if input.Truthy() {
		input.Call("focus")
		input.Call("select")
	}
}

func (a *app) renderSearchLocked() {
	clear(a.els.rightBody)
	panel := el("div", "panel search-panel")
	head := el("div", "search-head")
	input := el("input", "search code-search")
	input.Set("placeholder", "Search code across the workspace…")
	input.Set("spellcheck", false)
	input.Set("value", a.searchQuery)
	a.els.codeSearchInput = input
	on(input, "input", func(js.Value) {
		value := input.Get("value").String()
		a.mu.Lock()
		a.searchQuery = value
		a.mu.Unlock()
		a.scheduleCodeSearch(220 * time.Millisecond)
	})
	on(input, "keydown", func(ev js.Value) {
		if ev.Get("key").String() == "Enter" {
			ev.Call("preventDefault")
			a.scheduleCodeSearch(0)
		}
	})
	caseBtn := el("button", "case-btn")
	setText(caseBtn, "Aa")
	caseBtn.Set("title", "Match case")
	a.els.codeSearchCase = caseBtn
	on(caseBtn, "click", func(js.Value) {
		a.mu.Lock()
		a.searchCase = !a.searchCase
		a.mu.Unlock()
		a.scheduleCodeSearch(0)
	})
	clearBtn := el("button", "case-btn")
	setText(clearBtn, "×")
	clearBtn.Set("title", "Clear search")
	on(clearBtn, "click", func(js.Value) {
		a.mu.Lock()
		a.searchQuery = ""
		a.searchResults = nil
		a.searchErr = ""
		a.searchLoading = false
		a.searchGen++
		a.mu.Unlock()
		input.Set("value", "")
		a.mu.Lock()
		a.renderCodeSearchResultsLocked()
		a.renderRightTabs()
		a.mu.Unlock()
		input.Call("focus")
	})
	add(head, input, caseBtn, clearBtn)
	summary := el("div", "search-meta")
	a.els.codeSearchSummary = summary
	results := el("div", "search-results-list")
	a.els.codeSearchResults = results
	add(panel, head, summary, results)
	add(a.els.rightBody, panel)
	a.renderCodeSearchResultsLocked()
}

func (a *app) scheduleCodeSearch(delay time.Duration) {
	a.mu.Lock()
	a.searchGen++
	gen := a.searchGen
	query := a.searchQuery
	caseSensitive := a.searchCase
	if query == "" {
		a.searchResults = nil
		a.searchLoading = false
		a.searchErr = ""
		a.searchTruncated = false
		a.renderCodeSearchResultsLocked()
		a.renderRightTabs()
		a.mu.Unlock()
		return
	}
	a.searchLoading = true
	a.searchErr = ""
	a.renderCodeSearchResultsLocked()
	a.mu.Unlock()
	go func() {
		if delay > 0 {
			time.Sleep(delay)
		}
		a.mu.Lock()
		if gen != a.searchGen {
			a.mu.Unlock()
			return
		}
		a.mu.Unlock()
		resp, err := searchCode(query, caseSensitive)
		a.mu.Lock()
		if gen != a.searchGen {
			a.mu.Unlock()
			return
		}
		a.searchLoading = false
		if err != nil {
			a.searchErr = apiErrorHint(err)
			a.searchResults = nil
			a.searchTruncated = false
		} else {
			a.searchResults = resp.Matches
			a.searchFilesN = resp.Files
			a.searchScanned = resp.Scanned
			a.searchTookMS = resp.TookMS
			a.searchTruncated = resp.Truncated
		}
		if a.rightTab == "search" {
			a.renderCodeSearchResultsLocked()
		}
		a.renderRightTabs()
		a.mu.Unlock()
	}()
}

func (a *app) renderCodeSearchResultsLocked() {
	if !a.els.codeSearchResults.Truthy() || !a.els.codeSearchSummary.Truthy() {
		return
	}
	if a.els.codeSearchCase.Truthy() {
		cls := "case-btn"
		if a.searchCase {
			cls += " active"
		}
		a.els.codeSearchCase.Set("className", cls)
	}
	clear(a.els.codeSearchResults)
	summary := a.els.codeSearchSummary
	switch {
	case a.searchErr != "":
		setText(summary, "Search failed: "+a.searchErr)
		return
	case a.searchLoading:
		setText(summary, "Searching…")
		return
	case a.searchQuery == "":
		setText(summary, "Type to search every text file in the workspace.")
		return
	case len(a.searchResults) == 0:
		setText(summary, fmt.Sprintf("No matches for %q.", a.searchQuery))
		return
	default:
		text := fmt.Sprintf("%d matches in %d files · %d ms", len(a.searchResults), a.searchFilesN, a.searchTookMS)
		if a.searchTruncated {
			text += " · results truncated"
		}
		setText(summary, text)
	}
	for _, m := range a.searchResults {
		match := m
		row := el("div", "search-result")
		head := el("span", "search-result-head")
		path := el("span", "search-path")
		setText(path, match.Path)
		line := el("span", "search-line-num")
		setText(line, ":"+strconv.Itoa(match.Line))
		add(head, path, line)
		if match.Occurrences > 1 {
			occ := el("span", "search-occ")
			setText(occ, "×"+strconv.Itoa(match.Occurrences))
			add(head, occ)
		}
		preview := el("div", "search-preview")
		appendSearchPreview(preview, match.Preview, match.MatchStart, match.MatchLen)
		add(row, head, preview)
		on(row, "click", func(js.Value) { go a.openSearchResult(match) })
		add(a.els.codeSearchResults, row)
	}
}

func appendSearchPreview(parent js.Value, preview string, start, length int) {
	if length <= 0 || start < 0 || start >= len(preview) ||
		start+length > len(preview) {
		add(parent, text(preview))
		return
	}
	if start > 0 {
		add(parent, text(preview[:start]))
	}
	mark := el("span", "search-mark")
	setText(mark, preview[start:start+length])
	add(parent, mark)
	if start+length < len(preview) {
		add(parent, text(preview[start+length:]))
	}
}

func (a *app) openSearchResult(m CodeMatch) {
	if m.Line <= 0 {
		return
	}
	a.mu.Lock()
	pane := a.activePane
	a.mu.Unlock()
	a.openFileInPane(pane, m.Path, m.Line-1, 0, map[int]string{m.Line - 1: "search"})
}
