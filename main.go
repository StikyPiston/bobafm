package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

type Mode int
const (
	ModeBrowse Mode = iota
	ModeMounts
	ModeInput
	ModeView
)
type ClipboardMode int
const (
	ClipNone ClipboardMode = iota
	ClipCopy
	ClipCut
)

type model struct {
	mode Mode
	cwd  string

	showHidden bool
	browse     list.Model
	mounts     list.Model
	input      textinput.Model
	view       viewport.Model

	marked    map[string]bool
	clipboard []string
	clipMode  ClipboardMode

	editor      string
	inputTarget string
	keys        *keyMap
}

type keyMap struct {
	Up, Down, Enter, Mark, Yank, Cut, Paste,
	New, Rename, Delete, View, Mount, ToggleHidden,
	Filter, Quit, Back key.Binding
}

func newKeyMap() *keyMap {
	return &keyMap{
		Up: key.NewBinding(key.WithKeys("up","k"), key.WithHelp("↑/k","up")),
		Down: key.NewBinding(key.WithKeys("down","j"), key.WithHelp("↓/j","down")),
		Enter: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter","open/cd")),
		Mark: key.NewBinding(key.WithKeys(" "), key.WithHelp("space","mark/unmark")),
		Yank: key.NewBinding(key.WithKeys("y"), key.WithHelp("y","yank")),
		Cut: key.NewBinding(key.WithKeys("x"), key.WithHelp("x","cut")),
		Paste: key.NewBinding(key.WithKeys("p"), key.WithHelp("p","paste")),
		New: key.NewBinding(key.WithKeys("i"), key.WithHelp("i","new file/dir")),
		Rename: key.NewBinding(key.WithKeys("r"), key.WithHelp("r","rename")),
		Delete: key.NewBinding(key.WithKeys("d"), key.WithHelp("d","delete")),
		View: key.NewBinding(key.WithKeys("v"), key.WithHelp("v","view file")),
		Mount: key.NewBinding(key.WithKeys("m"), key.WithHelp("m","mount menu")),
		ToggleHidden: key.NewBinding(key.WithKeys("h"), key.WithHelp("h","toggle hidden")),
		Filter: key.NewBinding(key.WithKeys("/"), key.WithHelp("/","filter")),
		Quit: key.NewBinding(key.WithKeys("q"), key.WithHelp("q","quit")),
		Back: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc","back")),
	}
}

func (k *keyMap) all() []key.Binding {
	return []key.Binding{k.Up,k.Down,k.Enter,k.Mark,k.Yank,k.Cut,k.Paste,k.New,k.Rename,k.Delete,k.View,k.Mount,k.ToggleHidden,k.Filter,k.Back,k.Quit}
}

type fileItem struct {
	name string
	path string
	isDir bool
	isParent bool
}
func (i fileItem) Title() string { return i.name }
func (i fileItem) FilterValue() string { return i.name }
func (i fileItem) Description() string {
	desc := "File"
	if i.isParent { desc="Parent Directory" } else if i.isDir { desc="Directory" }
	if !i.isParent && app.marked[i.path] { desc += " [Marked]" }
	return desc
}

type mountItem struct { dev, mount string }
func (i mountItem) Title() string { return i.dev }
func (i mountItem) FilterValue() string { return i.dev }
func (i mountItem) Description() string {
	if i.mount=="" { return "Unmounted" }
	return "Mounted at "+i.mount
}

var app model

func initialModel() model {
	cwd,_ := os.Getwd()
	keys := newKeyMap()
	delegate := list.NewDefaultDelegate()
	delegate.ShowDescription = true

	b := list.New(nil, delegate, 0,0)
	b.SetFilteringEnabled(true)
	b.Title = "bobafm"
	b.AdditionalFullHelpKeys = func() []key.Binding { return keys.all() }

	m := list.New(nil, list.NewDefaultDelegate(),0,0)

	ti := textinput.New()
	ti.Placeholder = "new-file.txt or folder/"
	ti.Focus()

	vp := viewport.New(0,0)
	editor := os.Getenv("EDITOR")
	if editor=="" { editor="vi" }

	app = model{
		mode: ModeBrowse,
		cwd: cwd,
		showHidden: false,
		browse: b,
		mounts: m,
		input: ti,
		view: vp,
		marked: make(map[string]bool),
		editor: editor,
		keys: keys,
	}
	app.refreshBrowse()
	return app
}

func (m *model) refreshBrowse() {
	entries, err := os.ReadDir(m.cwd)
	if err != nil {
		return
	}

	var dirs []list.Item
	var files []list.Item

	parent := filepath.Dir(m.cwd)
	if parent != m.cwd {
		dirs = append(dirs, fileItem{name: "..", path: parent, isDir: true, isParent: true})
	}

	for _, e := range entries {
		if !m.showHidden && e.Name()[0] == '.' {
			continue
		}
		item := fileItem{name: e.Name(), path: filepath.Join(m.cwd, e.Name()), isDir: e.IsDir()}
		if e.IsDir() {
			dirs = append(dirs, item)
		} else {
			files = append(files, item)
		}
	}

	items := append(dirs, files...) // Directories first
	title := fmt.Sprintf("bobafm — %s", m.cwd)
	if m.showHidden {
		title += " (hidden)"
	}
	m.browse.Title = title
	m.browse.SetItems(items)
}

func (m *model) refreshMounts() {
	out,_ := exec.Command("lsblk","-nrpo","NAME,MOUNTPOINT").Output()
	items := []list.Item{}
	for _, line := range strings.Split(string(out),"\n") {
		f := strings.Fields(line)
		if len(f)==0 { continue }
		mi := mountItem{dev:f[0]}
		if len(f)>1 { mi.mount=f[1] }
		items = append(items,mi)
	}
	m.mounts.SetItems(items)
}

func (m *model) handleInput() {
	input := strings.TrimSpace(m.input.Value())
	if input=="" { return }
	switch m.inputTarget {
	case "create":
		full := filepath.Join(m.cwd,input)
		isDir := strings.HasSuffix(input,"/")
		parent := filepath.Dir(full)
		os.MkdirAll(parent,0755)
		if isDir { os.MkdirAll(full,0755) } else { f,_ := os.OpenFile(full,os.O_CREATE|os.O_EXCL,0644); if f!=nil { f.Close() } }
	case "rename":
		item := m.browse.SelectedItem().(fileItem)
		dst := filepath.Join(m.cwd,input)
		os.Rename(item.path,dst)
	}
	m.mode = ModeBrowse
	m.refreshBrowse()
}

func keys(m map[string]bool) []string {
	out := []string{}
	for k:=range m { out=append(out,k) }
	return out
}

func copyFile(src,dst string) {
	in,_ := os.Open(src); defer in.Close()
	out,_ := os.Create(dst); defer out.Close()
	io.Copy(out,in)
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.browse.SetSize(msg.Width,msg.Height-3)
		m.mounts.SetSize(msg.Width,msg.Height-3)
		m.view.Width = msg.Width
		m.view.Height = msg.Height-1
	case tea.KeyMsg:
		switch m.mode {
		case ModeBrowse:
			switch {
			case key.Matches(msg,m.keys.Quit): return m, tea.Quit
			case key.Matches(msg,m.keys.ToggleHidden): m.showHidden=!m.showHidden; m.refreshBrowse()
			case key.Matches(msg,m.keys.Enter):
				item := m.browse.SelectedItem().(fileItem)
				if item.isDir { m.cwd=item.path; m.refreshBrowse() } else { return m, tea.ExecProcess(exec.Command(m.editor,item.path),nil) }
			case key.Matches(msg,m.keys.Mark):
				item := m.browse.SelectedItem().(fileItem)
				if !item.isParent { m.marked[item.path]=!m.marked[item.path]; m.refreshBrowse() }
			case key.Matches(msg,m.keys.Yank): m.clipboard=keys(m.marked); m.clipMode=ClipCopy; m.marked=map[string]bool{}; m.refreshBrowse()
			case key.Matches(msg,m.keys.Cut): m.clipboard=keys(m.marked); m.clipMode=ClipCut; m.marked=map[string]bool{}; m.refreshBrowse()
			case key.Matches(msg,m.keys.Paste):
				for _,src:=range m.clipboard { dst:=filepath.Join(m.cwd,filepath.Base(src)); if m.clipMode==ClipCopy { copyFile(src,dst) } else { os.Rename(src,dst) } }
				m.clipboard=nil; m.clipMode=ClipNone; m.refreshBrowse()
			case key.Matches(msg,m.keys.New): m.mode=ModeInput; m.input.SetValue(""); m.inputTarget="create"
			case key.Matches(msg,m.keys.Rename):
				item := m.browse.SelectedItem().(fileItem)
				if !item.isParent { m.mode=ModeInput; m.input.SetValue(item.name); m.inputTarget="rename" }
			case key.Matches(msg,m.keys.Delete):
				item := m.browse.SelectedItem().(fileItem)
				if !item.isParent { os.RemoveAll(item.path); delete(m.marked,item.path); m.refreshBrowse() }
			case key.Matches(msg,m.keys.View):
				item := m.browse.SelectedItem().(fileItem)
				if !item.isParent && !item.isDir { data,_:=os.ReadFile(item.path); m.view.SetContent(string(data)); m.mode=ModeView }
			case key.Matches(msg,m.keys.Mount): m.refreshMounts(); m.mode=ModeMounts
			}
		case ModeMounts:
			switch {
			case key.Matches(msg,m.keys.Back): m.mode=ModeBrowse
			case key.Matches(msg,m.keys.Enter):
				item := m.mounts.SelectedItem().(mountItem)
				if item.mount=="" { exec.Command("udisksctl","mount","-b",item.dev).Run(); m.refreshMounts() } else { m.cwd=item.mount; m.refreshBrowse(); m.mode=ModeBrowse }
			case key.Matches(msg,m.keys.Back):
				item := m.mounts.SelectedItem().(mountItem)
				if item.mount!="" { exec.Command("udisksctl","unmount","-b",item.dev).Run(); m.refreshMounts() }
			}
		case ModeInput:
			switch {
			case key.Matches(msg,m.keys.Back): m.mode=ModeBrowse
			case key.Matches(msg,key.NewBinding(key.WithKeys("enter"))): m.handleInput()
			}
			m.input,cmd = m.input.Update(msg)
			return m, cmd
		case ModeView:
			if key.Matches(msg,m.keys.Back)||key.Matches(msg,m.keys.Quit) { m.mode=ModeBrowse }
			m.view,cmd = m.view.Update(msg)
			return m, cmd
		}
	}
	if m.mode==ModeBrowse { m.browse,cmd = m.browse.Update(msg) }
	if m.mode==ModeMounts { m.mounts,cmd = m.mounts.Update(msg) }
	return m,cmd
}

func (m model) View() string {
	switch m.mode {
	case ModeBrowse: return m.browse.View()
	case ModeMounts: return m.mounts.View()
	case ModeInput: return "Input:\n\n"+m.input.View()
	case ModeView: return m.view.View()
	}
	return ""
}

func main() {
	if err := tea.NewProgram(initialModel(), tea.WithAltScreen()).Start(); err!=nil {
		fmt.Println("Error running bobafm:", err)
		os.Exit(1)
	}
}
