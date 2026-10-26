//go:build linux && !js

// Package desktop embeds VibeDE's HTML/Go-WASM interface in a native GTK
// window using WebKitGTK. It calls GTK and WebKit directly through purego, so
// the application remains a cgo-free Go binary; the system provides the GUI
// runtime libraries.
package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// ErrCanceled is returned when the initial workspace chooser is dismissed.
var ErrCanceled = errors.New("workspace selection canceled")

// Options configures the native window.
type Options struct {
	Title       string
	Width       int
	Height      int
	InitialRoot string // when empty, a folder chooser is shown
	Start       func(root string) (url string, err error)
	Switch      func(root string) (url string, err error)
}

type api struct {
	gtkInitCheck    func(uintptr, uintptr) int32
	windowNew       func(int32) uintptr
	windowSetTitle  func(uintptr, string)
	windowSetSize   func(uintptr, int32, int32)
	windowSetPos    func(uintptr, int32)
	windowSetModal  func(uintptr, int32)
	windowPresent   func(uintptr)
	windowKeepAbove func(uintptr, int32)
	containerAdd    func(uintptr, uintptr)
	widgetShowAll   func(uintptr)
	widgetDestroy   func(uintptr)
	mainLoop        func()
	mainQuit        func()
	boxNew          func(int32, int32) uintptr
	boxPackStart    func(uintptr, uintptr, int32, int32, uint32)
	buttonNew       func(string) uintptr
	labelNew        func(string) uintptr
	labelSetText    func(uintptr, string)
	labelEllipsize  func(uintptr, int32)
	labelMaxChars   func(uintptr, int32)
	labelXAlign     func(uintptr, float32)
	chooserNew      func(string, uintptr, int32, string, int32, string, int32, uintptr) uintptr
	chooserSetDir   func(uintptr, string)
	chooserGetFile  func(uintptr) uintptr
	chooserMultiple func(uintptr, int32)
	dialogRun       func(uintptr) int32
	webViewNew      func(uintptr) uintptr
	webViewLoad     func(uintptr, string)
	webSettingsNew  func() uintptr
	webSettingsDev  func(uintptr, int32)
	signalConnect   func(uintptr, string, uintptr, uintptr, uintptr, uint32) uint64
	idleAdd         func(uintptr, uintptr) uint32
	gfree           func(uintptr)
}

func load() (*api, func(), error) {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return nil, nil, errors.New("no graphical display (DISPLAY/WAYLAND_DISPLAY unset)")
	}
	gtk, err := purego.Dlopen("libgtk-3.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, nil, fmt.Errorf("GTK3 runtime: %w", err)
	}
	web, err := purego.Dlopen("libwebkit2gtk-4.1.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		purego.Dlclose(gtk)
		return nil, nil, fmt.Errorf("WebKitGTK runtime: %w", err)
	}
	gobj, err := purego.Dlopen("libgobject-2.0.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		purego.Dlclose(gtk)
		purego.Dlclose(web)
		return nil, nil, fmt.Errorf("GObject runtime: %w", err)
	}
	glib, err := purego.Dlopen("libglib-2.0.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		purego.Dlclose(gtk)
		purego.Dlclose(web)
		purego.Dlclose(gobj)
		return nil, nil, fmt.Errorf("GLib runtime: %w", err)
	}
	a := &api{}
	purego.RegisterLibFunc(&a.gtkInitCheck, gtk, "gtk_init_check")
	purego.RegisterLibFunc(&a.windowNew, gtk, "gtk_window_new")
	purego.RegisterLibFunc(&a.windowSetTitle, gtk, "gtk_window_set_title")
	purego.RegisterLibFunc(&a.windowSetSize, gtk, "gtk_window_set_default_size")
	purego.RegisterLibFunc(&a.windowSetPos, gtk, "gtk_window_set_position")
	purego.RegisterLibFunc(&a.windowSetModal, gtk, "gtk_window_set_modal")
	purego.RegisterLibFunc(&a.windowPresent, gtk, "gtk_window_present")
	purego.RegisterLibFunc(&a.windowKeepAbove, gtk, "gtk_window_set_keep_above")
	purego.RegisterLibFunc(&a.containerAdd, gtk, "gtk_container_add")
	purego.RegisterLibFunc(&a.widgetShowAll, gtk, "gtk_widget_show_all")
	purego.RegisterLibFunc(&a.widgetDestroy, gtk, "gtk_widget_destroy")
	purego.RegisterLibFunc(&a.mainLoop, gtk, "gtk_main")
	purego.RegisterLibFunc(&a.mainQuit, gtk, "gtk_main_quit")
	purego.RegisterLibFunc(&a.boxNew, gtk, "gtk_box_new")
	purego.RegisterLibFunc(&a.boxPackStart, gtk, "gtk_box_pack_start")
	purego.RegisterLibFunc(&a.buttonNew, gtk, "gtk_button_new_with_label")
	purego.RegisterLibFunc(&a.labelNew, gtk, "gtk_label_new")
	purego.RegisterLibFunc(&a.labelSetText, gtk, "gtk_label_set_text")
	purego.RegisterLibFunc(&a.labelEllipsize, gtk, "gtk_label_set_ellipsize")
	purego.RegisterLibFunc(&a.labelMaxChars, gtk, "gtk_label_set_max_width_chars")
	purego.RegisterLibFunc(&a.labelXAlign, gtk, "gtk_label_set_xalign")
	purego.RegisterLibFunc(&a.chooserNew, gtk, "gtk_file_chooser_dialog_new")
	purego.RegisterLibFunc(&a.chooserSetDir, gtk, "gtk_file_chooser_set_current_folder")
	purego.RegisterLibFunc(&a.chooserGetFile, gtk, "gtk_file_chooser_get_filename")
	purego.RegisterLibFunc(&a.chooserMultiple, gtk, "gtk_file_chooser_set_select_multiple")
	purego.RegisterLibFunc(&a.dialogRun, gtk, "gtk_dialog_run")
	purego.RegisterLibFunc(&a.webViewNew, web, "webkit_web_view_new_with_settings")
	purego.RegisterLibFunc(&a.webViewLoad, web, "webkit_web_view_load_uri")
	purego.RegisterLibFunc(&a.webSettingsNew, web, "webkit_settings_new")
	purego.RegisterLibFunc(&a.webSettingsDev, web, "webkit_settings_set_enable_developer_extras")
	purego.RegisterLibFunc(&a.signalConnect, gobj, "g_signal_connect_data")
	purego.RegisterLibFunc(&a.idleAdd, glib, "g_idle_add")
	purego.RegisterLibFunc(&a.gfree, glib, "g_free")
	release := func() {
		purego.Dlclose(glib)
		purego.Dlclose(gobj)
		purego.Dlclose(web)
		purego.Dlclose(gtk)
	}
	return a, release, nil
}

// Available reports whether the GTK/WebKit runtime and a display exist.
func Available() bool {
	_, release, err := load()
	if err != nil {
		return false
	}
	release()
	return true
}

// Run opens the native window and blocks until it closes.
func Run(opts Options) error {
	if opts.Start == nil {
		return errors.New("desktop: Start callback is required")
	}
	if opts.Title == "" {
		opts.Title = "VibeDE"
	}
	if opts.Width <= 0 {
		opts.Width = 1600
	}
	if opts.Height <= 0 {
		opts.Height = 1000
	}
	a, release, err := load()
	if err != nil {
		return err
	}
	defer release()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if a.gtkInitCheck(0, 0) == 0 {
		return errors.New("GTK initialization failed")
	}

	root := opts.InitialRoot
	if root == "" {
		root, err = a.pickFolder(0, startDir(""))
		if err != nil {
			return err
		}
	}
	url, err := opts.Start(root)
	if err != nil {
		return err
	}

	win := a.windowNew(0)
	a.windowSetTitle(win, opts.Title+" — "+root)
	a.windowSetSize(win, int32(opts.Width), int32(opts.Height))
	a.windowSetPos(win, 1) // GTK_WIN_POS_CENTER

	vbox := a.boxNew(1, 0) // vertical
	hbox := a.boxNew(0, 8) // horizontal
	openBtn := a.buttonNew("Open Folder…")
	rootLabel := a.labelNew(root)
	a.labelEllipsize(rootLabel, 3) // Pango ellipsize END
	a.labelMaxChars(rootLabel, 90)
	a.labelXAlign(rootLabel, 0)
	a.boxPackStart(hbox, openBtn, 0, 0, 0)
	a.boxPackStart(hbox, rootLabel, 1, 1, 0)
	a.boxPackStart(vbox, hbox, 0, 0, 0)

	settings := a.webSettingsNew()
	if os.Getenv("VIBEDE_WEBKIT_DEVTOOLS") != "" {
		a.webSettingsDev(settings, 1)
	}
	view := a.webViewNew(settings)
	a.webViewLoad(view, url)
	a.boxPackStart(vbox, view, 1, 1, 0)
	a.containerAdd(win, vbox)

	destroy := purego.NewCallback(func(uintptr, uintptr) {
		a.mainQuit()
	})
	a.signalConnect(win, "destroy", destroy, 0, 0, 0)

	open := purego.NewCallback(func(uintptr, uintptr) {
		next, err := a.pickFolder(win, startDir(root))
		if err != nil || next == "" {
			if err != nil && !errors.Is(err, ErrCanceled) {
				a.labelSetText(rootLabel, "Workspace selection failed: "+err.Error())
			}
			return
		}
		if opts.Switch == nil {
			a.labelSetText(rootLabel, next)
			return
		}
		nextURL, err := opts.Switch(next)
		if err != nil {
			a.labelSetText(rootLabel, "Cannot open "+next+": "+err.Error())
			return
		}
		a.webViewLoad(view, nextURL)
		root = next
		a.windowSetTitle(win, opts.Title+" — "+next)
		a.labelSetText(rootLabel, next)
	})
	a.signalConnect(openBtn, "clicked", open, 0, 0, 0)

	setDesktopAPI(a)
	a.widgetShowAll(win)
	a.windowPresent(win)
	a.mainLoop()
	setDesktopAPI(nil)
	return nil
}

type windowSpec struct {
	url   string
	title string
}

var (
	desktopMu    sync.Mutex
	desktopAPI   *api
	desktopSeq   uintptr
	desktopQueue = make(map[uintptr]windowSpec)
	desktopCBs   []uintptr
)

func setDesktopAPI(a *api) {
	desktopMu.Lock()
	desktopAPI = a
	desktopMu.Unlock()
}

// OpenWindow opens another native WebKit window on the same internal server.
// It is safe to call from an HTTP handler; the actual GTK work is scheduled on
// the GTK main loop with g_idle_add.
func OpenWindow(target, title string) error {
	if target == "" {
		return errors.New("detached window URL is empty")
	}
	desktopMu.Lock()
	a := desktopAPI
	if a == nil {
		desktopMu.Unlock()
		return errors.New("native desktop is not running")
	}
	desktopSeq++
	id := desktopSeq
	desktopQueue[id] = windowSpec{url: target, title: title}
	desktopMu.Unlock()

	cb := purego.NewCallback(func(data uintptr) int32 {
		desktopMu.Lock()
		spec, ok := desktopQueue[data]
		delete(desktopQueue, data)
		api := desktopAPI
		desktopMu.Unlock()
		if ok && api != nil {
			api.openDetachedWindow(spec)
		}
		return 0 // G_SOURCE_REMOVE
	})
	desktopMu.Lock()
	desktopCBs = append(desktopCBs, cb)
	api := desktopAPI
	desktopMu.Unlock()
	if api == nil {
		return errors.New("native desktop is not running")
	}
	if api.idleAdd(cb, id) == 0 {
		return errors.New("could not schedule native window")
	}
	return nil
}

func (a *api) openDetachedWindow(spec windowSpec) {
	title := spec.title
	if title == "" {
		title = "VibeDE — detached"
	}
	win := a.windowNew(0)
	a.windowSetTitle(win, title)
	a.windowSetSize(win, 1100, 820)
	a.windowSetPos(win, 1)
	settings := a.webSettingsNew()
	if os.Getenv("VIBEDE_WEBKIT_DEVTOOLS") != "" {
		a.webSettingsDev(settings, 1)
	}
	view := a.webViewNew(settings)
	a.webViewLoad(view, spec.url)
	a.containerAdd(win, view)
	a.widgetShowAll(win)
	a.windowPresent(win)
}

func (a *api) pickFolder(parent uintptr, initial string) (string, error) {
	dlg := a.chooserNew(
		"Select a VibeDE workspace",
		parent,
		2,            // GTK_FILE_CHOOSER_ACTION_SELECT_FOLDER
		"Cancel", -6, // GTK_RESPONSE_CANCEL
		"Open", -3, // GTK_RESPONSE_ACCEPT
		0,
	)
	if dlg == 0 {
		return "", errors.New("could not create folder chooser")
	}
	a.chooserMultiple(dlg, 0)
	if initial != "" {
		if st, err := os.Stat(initial); err == nil && st.IsDir() {
			a.chooserSetDir(dlg, initial)
		}
	}
	a.windowSetSize(dlg, 900, 600)
	a.windowSetModal(dlg, 1)
	a.windowSetPos(dlg, 1)
	a.windowKeepAbove(dlg, 1)
	a.widgetShowAll(dlg)
	a.windowPresent(dlg)
	response := a.dialogRun(dlg)
	selected := ""
	if response == -3 {
		if ptr := a.chooserGetFile(dlg); ptr != 0 {
			selected = goString(ptr)
			a.gfree(ptr)
		}
	}
	a.widgetDestroy(dlg)
	if selected == "" {
		return "", ErrCanceled
	}
	st, err := os.Stat(selected)
	if err != nil || !st.IsDir() {
		return "", fmt.Errorf("%s is not a directory", selected)
	}
	return filepath.Clean(selected), nil
}

func startDir(root string) string {
	if root != "" {
		if st, err := os.Stat(root); err == nil && st.IsDir() {
			return root
		}
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	home, _ := os.UserHomeDir()
	return home
}

func goString(c uintptr) string {
	if c == 0 {
		return ""
	}
	// Take the address and dereference to keep go vet's unsafeptr analyzer
	// happy; this is the standard C-string conversion pattern used by purego.
	ptr := *(*unsafe.Pointer)(unsafe.Pointer(&c))
	length := 0
	for *(*byte)(unsafe.Add(ptr, uintptr(length))) != 0 {
		length++
		if length > 1<<16 {
			break
		}
	}
	return string(unsafe.Slice((*byte)(ptr), length))
}
