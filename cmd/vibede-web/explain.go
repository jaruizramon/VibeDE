//go:build js && wasm

package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"syscall/js"
	"time"
)

// Selection explanations: selecting code opens a small popover anchored to the
// selection. The user asks a question there; the server forwards the selection,
// the selected file and the workspace files it links to via imports to the
// configured small model.

const (
	explainMaxLines = 300
	explainWidth    = 372
)

type explainTarget struct {
	Path      string
	Language  string
	Function  string
	StartLine int // 1-based
	EndLine   int // 1-based
	Code      string
	left      float64
	top       float64
}

type explainState struct {
	target      explainTarget
	answer      string
	model       string
	context     []string
	annotations []Annotation
	errMsg      string
	loading     bool
	gen         int
	left        float64
	top         float64
}

// ctrlExplain opens the popover for the current selection and focuses its
// question field. With the popover already open it just refocuses the field.
func (a *app) ctrlExplain() {
	if a.explainPop.Truthy() {
		a.focusExplainInput()
		return
	}
	target, ok := a.selectionTarget()
	if !ok {
		a.toast("Select some code first.", "warn")
		return
	}
	a.showExplain(target)
	a.focusExplainInput()
}

// bindExplain opens the popover whenever a fresh selection is made in an
// editor pane, and hides it when the selection is cleared elsewhere. It does
// not take focus: focusing an input would clear the visible selection.
func (a *app) bindExplain() {
	pick := func(ev js.Value) {
		if a.explainPop.Truthy() && a.explainPop.Call("contains", ev.Get("target")).Bool() {
			return
		}
		if target, ok := a.selectionTarget(); ok {
			a.showExplain(target)
		}
	}
	on(doc(), "mouseup", func(ev js.Value) { pick(ev) })
	on(doc(), "keyup", func(ev js.Value) {
		if ev.Get("shiftKey").Bool() {
			pick(ev)
		}
	})
	on(doc(), "mousedown", func(ev js.Value) {
		if a.explainPop.Truthy() && a.explainPop.Call("contains", ev.Get("target")).Bool() {
			return
		}
		a.hideExplain()
	})
}

func (a *app) showExplain(target explainTarget) {
	a.mu.Lock()
	a.explain.gen++
	a.explain = explainState{target: target, left: target.left, top: target.top, gen: a.explain.gen}
	a.mu.Unlock()
	a.renderExplainPopup()
}

// selectionTarget returns the current DOM selection when it lies inside an
// editor pane. The editor text is taken from the tab's authoritative LinesText,
// not from the DOM, so line numbers and badges never leak into the snippet.
func (a *app) selectionTarget() (explainTarget, bool) {
	for i := range a.panes {
		if !a.paneVisible(i) {
			continue
		}
		if t, ok := a.captureSelection(i); ok {
			return t, true
		}
	}
	return explainTarget{}, false
}

func (a *app) captureSelection(paneIdx int) (explainTarget, bool) {
	var out explainTarget
	pane := a.paneAt(paneIdx)
	if pane == nil || !pane.code.Truthy() {
		return out, false
	}
	sel := js.Global().Call("getSelection")
	if sel.IsNull() || sel.IsUndefined() || sel.Get("rangeCount").Int() == 0 || sel.Get("isCollapsed").Bool() {
		return out, false
	}
	rng := sel.Call("getRangeAt", 0)
	startNode := rng.Get("startContainer")
	endNode := rng.Get("endContainer")
	if !pane.code.Call("contains", startNode).Bool() || !pane.code.Call("contains", endNode).Bool() {
		return out, false
	}
	startRow, startCode, ok := rowAndCode(startNode)
	if !ok {
		return out, false
	}
	endRow, endCode, ok := rowAndCode(endNode)
	if !ok {
		return out, false
	}
	startLine, err := strconv.Atoi(getData(startRow, "line"))
	if err != nil {
		return out, false
	}
	endLine, err := strconv.Atoi(getData(endRow, "line"))
	if err != nil {
		return out, false
	}
	t := pane.tabs[pane.active]
	if t == nil || len(t.LinesText) == 0 {
		return out, false
	}
	if startLine > endLine {
		startLine, endLine = endLine, startLine
		startCode, endCode = endCode, startCode
	}
	if startLine < 0 {
		startLine = 0
	}
	if endLine >= len(t.LinesText) {
		endLine = len(t.LinesText) - 1
	}
	if startLine > endLine {
		return out, false
	}
	startCol := 0
	if startCode.Call("contains", startNode).Bool() {
		startCol = textLength(startCode, startNode, rng.Get("startOffset").Int())
	}
	endCol := utf16Len(t.LinesText[endLine])
	if endCode.Call("contains", endNode).Bool() {
		endCol = textLength(endCode, endNode, rng.Get("endOffset").Int())
	}
	if startLine == endLine && endCol <= startCol {
		return out, false
	}
	truncated := false
	if endLine-startLine+1 > explainMaxLines {
		endLine = startLine + explainMaxLines - 1
		endCol = utf16Len(t.LinesText[endLine])
		truncated = true
	}
	var b strings.Builder
	if startLine == endLine {
		b.WriteString(sliceUTF16(t.LinesText[startLine], startCol, endCol))
	} else {
		b.WriteString(sliceUTF16(t.LinesText[startLine], startCol, utf16Len(t.LinesText[startLine])))
		for i := startLine + 1; i < endLine; i++ {
			b.WriteByte('\n')
			b.WriteString(t.LinesText[i])
		}
		b.WriteByte('\n')
		b.WriteString(sliceUTF16(t.LinesText[endLine], 0, endCol))
	}
	if truncated {
		b.WriteString("\n… (selection truncated)")
	}
	out = explainTarget{
		Path:      t.Path,
		Language:  t.File.Language,
		Function:  enclosingFunction(*t, startLine, endLine),
		StartLine: startLine + 1,
		EndLine:   endLine + 1,
		Code:      b.String(),
	}
	rect := rng.Call("getBoundingClientRect")
	out.left, out.top = popupPosition(rect)
	return out, true
}

// rowAndCode walks up from a selection endpoint to the rendered code row and
// returns that row plus its text span.
func rowAndCode(node js.Value) (js.Value, js.Value, bool) {
	for cur := node; cur.Truthy(); cur = cur.Get("parentNode") {
		if cur.Get("nodeType").Int() != 1 {
			continue
		}
		if !cur.Call("hasAttribute", "data-line").Bool() {
			continue
		}
		code := cur.Call("querySelector", ".line-code")
		if !code.Truthy() {
			return js.Value{}, js.Value{}, false
		}
		return cur, code, true
	}
	return js.Value{}, js.Value{}, false
}

// textLength counts UTF-16 code units between the start of lineCode and the
// given DOM point, which is the unit used by Go's selection offsets below.
func textLength(lineCode, node js.Value, offset int) int {
	r := doc().Call("createRange")
	r.Call("setStart", lineCode, 0)
	r.Call("setEnd", node, offset)
	return utf16Len(r.Call("toString").String())
}

func popupPosition(rect js.Value) (float64, float64) {
	w := js.Global().Get("innerWidth").Float()
	h := js.Global().Get("innerHeight").Float()
	left := rect.Get("left").Float() + rect.Get("width").Float()/2 - explainWidth/2
	if left < 8 {
		left = 8
	}
	if left+explainWidth > w-8 {
		left = w - explainWidth - 8
	}
	top := rect.Get("bottom").Float() + 8
	if top+260 > h-8 {
		top = rect.Get("top").Float() - 268
	}
	if top < 8 {
		top = 8
	}
	return left, top
}

func enclosingFunction(t tab, startLine, endLine int) string {
	best := ""
	bestSpan := 0
	for _, fn := range t.Analysis.Functions {
		if fn.StartLine-1 > startLine || fn.EndLine-1 < endLine {
			continue
		}
		span := fn.EndLine - fn.StartLine
		if best == "" || span < bestSpan {
			best = fnKey(fn)
			bestSpan = span
		}
	}
	return best
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// sliceUTF16 slices s by UTF-16 code-unit offsets, the unit browsers report.
func sliceUTF16(s string, lo, hi int) string {
	if lo < 0 {
		lo = 0
	}
	if hi < lo {
		hi = lo
	}
	var b strings.Builder
	pos := 0
	for _, r := range s {
		n := 1
		if r > 0xFFFF {
			n = 2
		}
		if pos >= hi {
			break
		}
		if pos+n > lo {
			b.WriteRune(r)
		}
		pos += n
	}
	return b.String()
}

func (a *app) hideExplain() {
	if !a.explainPop.Truthy() {
		return
	}
	a.explainPop.Call("remove")
	a.explainPop = js.Value{}
	a.mu.Lock()
	a.explain.gen++ // drop any in-flight answer
	a.explain.loading = false
	a.mu.Unlock()
}

func (a *app) focusExplainInput() {
	if !a.explainPop.Truthy() {
		return
	}
	if in := a.explainPop.Call("querySelector", ".explain-input"); in.Truthy() {
		in.Call("focus")
	}
}

func (a *app) renderExplainPopup() {
	a.mu.Lock()
	st := a.explain
	ai := a.info.AI
	a.mu.Unlock()

	if !a.explainPop.Truthy() {
		a.explainPop = el("div", "explain-pop")
		add(doc().Get("body"), a.explainPop)
	}
	pop := a.explainPop
	clear(pop)
	pop.Set("style", fmt.Sprintf("position:fixed;left:%.0fpx;top:%.0fpx;z-index:65", st.left, st.top))

	head := el("div", "explain-head")
	rangeEl := el("span", "explain-range")
	label := st.target.Path
	if label == "" {
		label = "selection"
	}
	if st.target.StartLine > 0 {
		if st.target.StartLine == st.target.EndLine {
			label = fmt.Sprintf("%s:%d", label, st.target.StartLine)
		} else {
			label = fmt.Sprintf("%s:%d-%d", label, st.target.StartLine, st.target.EndLine)
		}
	}
	setText(rangeEl, label)
	rangeEl.Set("title", label)
	spacer := el("span", "spacer")
	closeBtn := el("button", "close-x")
	setText(closeBtn, "×")
	closeBtn.Set("title", "Close (Esc)")
	on(closeBtn, "click", func(js.Value) { a.hideExplain() })
	add(head, rangeEl, spacer, closeBtn)

	ask := el("div", "explain-ask")
	input := el("input", "explain-input")
	input.Set("placeholder", "Ask about this code, or Enter to explain…")
	input.Set("spellcheck", false)
	askBtn := el("button", "tool-btn")
	setText(askBtn, "Ask")
	submit := func(js.Value) {
		question := strings.TrimSpace(input.Get("value").String())
		a.askExplain(question)
	}
	on(askBtn, "click", submit)
	on(input, "keydown", func(ev js.Value) {
		if ev.Get("key").String() == "Enter" && !ev.Get("shiftKey").Bool() {
			ev.Call("preventDefault")
			submit(ev)
		}
	})
	add(ask, input, askBtn)

	meta := el("div", "explain-meta")
	body := el("div", "explain-body")
	switch {
	case !ai.Enabled:
		setText(meta, "AI explanations are off")
		hint := el("div", "explain-err")
		setText(hint, "Set ai.endpoint in .vibede.json (or start with --ai-url) and reload.")
		add(body, hint)
	case st.loading:
		name := ai.Model
		if name == "" {
			name = "model"
		}
		setText(meta, name+" · thinking…")
		loading := el("div", "explain-loading")
		setText(loading, "Reading this file and its linked imports…")
		add(body, loading)
	case st.errMsg != "":
		setText(meta, "request failed")
		errEl := el("div", "explain-err")
		setText(errEl, st.errMsg)
		add(body, errEl)
	case st.answer != "":
		setText(meta, explainMetaText(st))
		answer := el("pre", "explain-answer")
		setText(answer, st.answer)
		add(body, answer)
		if len(st.annotations) > 0 {
			ex := ExplainRecord{
				Path:        st.target.Path,
				StartLine:   st.target.StartLine,
				EndLine:     st.target.EndLine,
				Annotations: st.annotations,
			}
			btn := el("button", "tool-btn inline-notes-btn")
			setText(btn, fmt.Sprintf("Show %d line notes", len(st.annotations)))
			btn.Set("title", "Place these notes above their code lines")
			on(btn, "click", func(js.Value) {
				a.hideExplain()
				go a.applyAnnotations(ex)
			})
			add(body, btn)
		}
	default:
		setText(meta, "The model sees this file and the files it imports.")
		if st.target.Function != "" {
			setText(meta, "Inside "+st.target.Function+" · the model sees this file and its imports.")
		}
		hint := el("div", "explain-hint")
		setText(hint, "Type a question and press Enter. Empty means “explain this”.")
		add(body, hint)
	}

	add(pop, head, ask, meta, body)
}

func explainMetaText(st explainState) string {
	parts := []string{st.model}
	if len(st.context) > 0 {
		noun := "files"
		if len(st.context) == 1 {
			noun = "file"
		}
		parts = append(parts, fmt.Sprintf("%d %s: %s", len(st.context), noun, strings.Join(st.context, ", ")))
	}
	return strings.Join(parts, " · ")
}

// askExplain sends the question for the current selection. An empty question
// asks the server for a general explanation.
func (a *app) askExplain(question string) {
	a.mu.Lock()
	target := a.explain.target
	enabled := a.info.AI.Enabled
	if target.Code == "" {
		a.mu.Unlock()
		return
	}
	a.explain.gen++
	gen := a.explain.gen
	a.explain.loading = enabled
	a.explain.errMsg = ""
	a.explain.answer = ""
	if !enabled {
		a.explain.errMsg = "AI explanations are off. Set ai.endpoint in .vibede.json or start with --ai-url."
	}
	a.mu.Unlock()
	a.renderExplainPopup()
	if !enabled {
		return
	}

	go func() {
		req := ExplainRequest{
			Path:      target.Path,
			Language:  target.Language,
			Function:  target.Function,
			StartLine: target.StartLine,
			EndLine:   target.EndLine,
			Code:      target.Code,
			Question:  question,
		}
		var resp ExplainResponse
		data, _, apiErr := requestWithTimeout("POST", "/api/explain", req, 5*time.Minute)
		if apiErr == nil {
			apiErr = json.Unmarshal(data, &resp)
		}

		a.mu.Lock()
		if gen != a.explain.gen {
			a.mu.Unlock()
			return
		}
		a.explain.loading = false
		if apiErr != nil {
			a.explain.errMsg = apiErr.Error()
		} else {
			a.explain.answer = resp.Explanation
			a.explain.model = resp.Model
			a.explain.context = resp.Context
			a.explain.annotations = resp.Annotations
			rec := ExplainRecord{
				At:          time.Now().UTC(),
				Path:        target.Path,
				Language:    target.Language,
				Function:    target.Function,
				StartLine:   target.StartLine,
				EndLine:     target.EndLine,
				Question:    resp.Question,
				Answer:      resp.Explanation,
				Model:       resp.Model,
				Context:     resp.Context,
				Annotations: resp.Annotations,
			}
			a.explanations = append([]ExplainRecord{rec}, a.explanations...)
			if len(a.explanations) > 200 {
				a.explanations = a.explanations[:200]
			}
			a.archive.Explanations = len(a.explanations)
			a.archive.Changes = len(a.changes)
			a.renderRightTabs()
			if a.rightTab == "activity" {
				a.renderRight()
			}
		}
		a.mu.Unlock()
		a.renderExplainPopup()
	}()
}

// applyAnnotations places an explanation's line notes above their code lines
// and opens the file. Notes are keyed by path, so they survive tab reloads
// until a note is dismissed or another explanation replaces them.
func (a *app) applyAnnotations(ex ExplainRecord) {
	if ex.Path == "" {
		return
	}
	line := ex.StartLine - 1
	if line < 0 {
		line = 0
	}
	if len(ex.Annotations) > 0 {
		a.mu.Lock()
		a.notes[ex.Path] = append([]Annotation(nil), ex.Annotations...)
		a.mu.Unlock()
	}
	a.openFile(ex.Path, line, 0, true)
}

// codeNoteElement renders one explanation note between code lines. It has no
// line number and does not shift the file's line numbering.
func (a *app) codeNoteElement(paneIdx int, path string, idx int, note Annotation) js.Value {
	row := el("div", "code-note")
	tag := el("span", "code-note-tag")
	label := fmt.Sprintf("line %d", note.StartLine)
	if note.EndLine > note.StartLine {
		label = fmt.Sprintf("lines %d-%d", note.StartLine, note.EndLine)
	}
	setText(tag, label)
	body := el("span", "code-note-text")
	setText(body, note.Text)
	x := el("button", "code-note-x")
	setText(x, "×")
	x.Set("title", "Hide this note")
	on(x, "click", func(ev js.Value) {
		ev.Call("stopPropagation")
		a.removeNote(paneIdx, path, idx)
	})
	add(row, tag, body, x)
	return row
}

func (a *app) removeNote(paneIdx int, path string, idx int) {
	a.mu.Lock()
	notes := a.notes[path]
	if idx >= 0 && idx < len(notes) {
		notes = append(append([]Annotation(nil), notes[:idx]...), notes[idx+1:]...)
		if len(notes) == 0 {
			delete(a.notes, path)
		} else {
			a.notes[path] = notes
		}
	}
	a.renderEditorLocked(paneIdx)
	a.mu.Unlock()
}
