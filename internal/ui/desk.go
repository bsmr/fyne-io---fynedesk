package ui

import (
	"math"
	"os/exec"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	deskDriver "fyne.io/fyne/v2/driver/desktop"

	"fyne.io/fynedesk"
	"fyne.io/fynedesk/wm"
)

const (
	// RootWindowName is the base string that all root windows will have in their title and is used to identify root windows.
	RootWindowName = "Fyne Desktop"
	// SkipTaskbarHint should be added to the title of normal windows that should be skipped like the X11 SkipTaskbar hint.
	SkipTaskbarHint = "FyneDesk:skip"
)

type desktop struct {
	wm.ShortcutHandler
	app      fyne.App
	wm       fynedesk.WindowManager
	icons    fynedesk.ApplicationProvider
	recent   []fynedesk.AppData
	screens  fynedesk.ScreenList
	settings fynedesk.DeskSettings

	run         func()
	showMenu    func(*fyne.Menu, fyne.Position)
	moduleCache []fynedesk.Module

	bar     *bar
	widgets *widgetPanel
	mouse   fyne.CanvasObject
	root    fyne.Window
}

func (l *desktop) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	bg := objects[0].(*background)
	bg.Resize(size)

	barHeight := l.bar.MinSize().Height
	l.bar.Resize(fyne.NewSize(size.Width, barHeight+1)) // add 1 so rounding cannot trigger mouse out on bottom edge
	l.bar.Move(fyne.NewPos(0, size.Height-barHeight))
	l.bar.Refresh()

	widgetsWidth := l.widgets.MinSize().Width
	l.widgets.Resize(fyne.NewSize(widgetsWidth, size.Height))
	l.widgets.Move(fyne.NewPos(size.Width-widgetsWidth, 0))
	l.widgets.Refresh()
}

func (l *desktop) MinSize(_ []fyne.CanvasObject) fyne.Size {
	return fyne.NewSize(640, 480) // tiny - window manager will scale up to screen size
}

func (l *desktop) ShowMenuAt(menu *fyne.Menu, pos fyne.Position) {
	l.showMenu(menu, pos)
}

func (l *desktop) updateBackgrounds(path string) {
	l.root.Content().(*fyne.Container).Objects[0].(*background).updateBackground(path)
}

func (l *desktop) createPrimaryContent() fyne.CanvasObject {
	l.bar = newBar(l)
	l.widgets = newWidgetPanel(l)
	l.mouse = newMouse()
	l.mouse.Hide()

	return container.New(l, newBackground(), l.bar, l.widgets, l.mouse)
}

func (l *desktop) createRoot(screens fynedesk.ScreenList) fyne.Window {
	win := l.newDesktopWindowFull()

	win.SetContent(l.createPrimaryContent())

	return win
}

func (l *desktop) setupRoot() {
	if l.root == nil {
		l.root = l.createRoot(l.screens)
	}

	scale := l.screens.Primary().CanvasScale()
	l.root.Resize(fyne.NewSize(float32(l.screens.Primary().Width)/scale, float32(l.screens.Primary().Height)/scale))
}

func (l *desktop) RecentApps() []fynedesk.AppData {
	return l.recent
}

func (l *desktop) Run() {
	go l.wm.Run()
	l.run() // use the configured run method
}

func (l *desktop) RunApp(app fynedesk.AppData) error {
	vars := l.scaleVars(l.Screens().Active().CanvasScale())
	err := app.Run(vars)

	if err == nil {
		l.recent = append([]fynedesk.AppData{app}, l.recent...)
		// remove if it was already on the list
		for i := 1; i < len(l.recent); i++ {
			if l.recent[i] == app {
				if i == len(l.recent)-1 {
					l.recent = l.recent[:i]
				} else {
					l.recent = append(l.recent[:i], l.recent[i+1:]...)
				}
				break
			}
		}
		// limit to 5 items
		if len(l.recent) > 5 {
			l.recent = l.recent[:5]
		}
		l.settings.(*deskSettings).saveRecents()
	}
	return err
}

func (l *desktop) Settings() fynedesk.DeskSettings {
	return l.settings
}

func (l *desktop) ContentSizePixels(screen *fynedesk.Screen) (uint32, uint32) {
	screenW := uint32(screen.Width)
	screenH := uint32(screen.Height)
	if l.screens.Primary() == screen {
		return screenW - uint32(widgetPanelWidth*screen.CanvasScale()), screenH
	}
	return screenW, screenH
}

func (l *desktop) IconProvider() fynedesk.ApplicationProvider {
	return l.icons
}

func (l *desktop) WindowManager() fynedesk.WindowManager {
	return l.wm
}

func (l *desktop) clearModuleCache() {
	for _, mod := range l.moduleCache {
		mod.Destroy()
	}

	l.moduleCache = nil
}

func (l *desktop) Modules() []fynedesk.Module {
	if l.moduleCache != nil {
		return l.moduleCache
	}

	var mods []fynedesk.Module
	for _, meta := range fynedesk.AvailableModules() {
		if !isModuleEnabled(meta.Name, l.settings) {
			continue
		}

		instance := meta.NewInstance()
		mods = append(mods, instance)

		if bind, ok := instance.(fynedesk.KeyBindModule); ok {
			for sh, f := range bind.Shortcuts() {
				l.AddShortcut(sh, f)
			}
		}
	}

	l.moduleCache = mods
	return mods
}

func (l *desktop) qtScreenScales() string {
	screenScales := ""
	for i, screen := range l.Screens().Screens() {
		if i > 0 {
			screenScales += ";"
		}
		// Qt toolkit cannot handle scale < 1
		positiveScale := math.Max(1.0, float64(screen.CanvasScale()))
		screenScales += screen.Name + "=" + strconv.FormatFloat(positiveScale, 'f', 1, 32)
	}
	return screenScales
}

func (l *desktop) scaleVars(scale float32) []string {
	intScale := int(math.Round(float64(scale)))

	return []string{
		"QT_SCREEN_SCALE_FACTORS=" + l.qtScreenScales(),
		"GDK_SCALE=" + strconv.Itoa(intScale),
		"ELM_SCALE=" + strconv.FormatFloat(float64(scale), 'f', 1, 32),
	}
}

// MouseInNotify can be called by the window manager to alert the desktop that the cursor has entered the canvas
func (l *desktop) MouseInNotify(pos fyne.Position) {
	if l.bar == nil {
		return
	}
	mouseX, mouseY := pos.X, pos.Y
	barX, barY := l.bar.Position().X, l.bar.Position().Y
	barWidth, barHeight := l.bar.Size().Width, l.bar.Size().Height
	if mouseX >= barX && mouseX <= barX+barWidth {
		if mouseY >= barY && mouseY <= barY+barHeight {
			l.bar.MouseIn(&deskDriver.MouseEvent{PointEvent: fyne.PointEvent{AbsolutePosition: pos, Position: pos}})
		}
	}
}

// MouseOutNotify can be called by the window manager to alert the desktop that the cursor has left the canvas
func (l *desktop) MouseOutNotify() {
	if l.bar == nil {
		return
	}
	l.bar.MouseOut()
}

func (l *desktop) startSettingsChangeListener(settings chan fynedesk.DeskSettings) {
	for {
		s := <-settings
		l.clearModuleCache()
		l.updateBackgrounds(s.Background())
		l.widgets.reloadModules(l.Modules())

		l.bar.iconSize = float32(l.Settings().LauncherIconSize())
		l.bar.iconScale = float32(l.Settings().LauncherZoomScale())
		l.bar.disableZoom = l.Settings().LauncherDisableZoom()
		l.bar.updateIcons()
		l.bar.updateIconOrder()
		l.bar.updateTaskbar()
	}
}

func (l *desktop) addSettingsChangeListener() {
	listener := make(chan fynedesk.DeskSettings)
	l.Settings().AddChangeListener(listener)
	go l.startSettingsChangeListener(listener)
}

func (l *desktop) registerShortcuts() {
	l.AddShortcut(fynedesk.NewShortcut("Show Launcher", fyne.KeySpace, fynedesk.UserModifier),
		ShowAppLauncher)
	l.AddShortcut(fynedesk.NewShortcut("Switch App Next", fyne.KeyTab, fynedesk.UserModifier),
		func() {
			// dummy - the wm handles app switcher
		})
	l.AddShortcut(fynedesk.NewShortcut("Switch App Previous", fyne.KeyTab, fynedesk.UserModifier|deskDriver.ShiftModifier),
		func() {
			// dummy - the wm handles app switcher
		})
	fynedesk.Instance().AddShortcut(&fynedesk.Shortcut{Name: "Print Window", KeyName: deskDriver.KeyPrintScreen,
		Modifier: deskDriver.ShiftModifier},
		l.screenshotWindow)
	fynedesk.Instance().AddShortcut(&fynedesk.Shortcut{Name: "Print Screen", KeyName: deskDriver.KeyPrintScreen},
		l.screenshot)
	fynedesk.Instance().AddShortcut(&fynedesk.Shortcut{Name: "Calculator", KeyName: fynedesk.KeyCalculator},
		l.calculator)
}

// Screens returns the screens provider of the current desktop environment for access to screen functionality.
func (l *desktop) Screens() fynedesk.ScreenList {
	return l.screens
}

// NewDesktop creates a new desktop in fullscreen for main usage.
// The WindowManager passed in will be used to manage the screen it is loaded on.
// An ApplicationProvider is used to lookup application icons from the operating system.
func NewDesktop(app fyne.App, wm fynedesk.WindowManager, icons fynedesk.ApplicationProvider, screenProvider fynedesk.ScreenList) fynedesk.Desktop {
	desk := newDesktop(app, wm, icons)
	desk.run = desk.runFull
	screenProvider.AddChangeListener(desk.setupRoot)
	desk.screens = screenProvider

	desk.setupRoot()
	return desk
}

// NewEmbeddedDesktop creates a new windowed desktop for test purposes.
// An ApplicationProvider is used to lookup application icons from the operating system.
// If run during CI for testing it will return an in-memory window using the
// fyne/test package.
func NewEmbeddedDesktop(app fyne.App, icons fynedesk.ApplicationProvider) fynedesk.Desktop {
	desk := newDesktop(app, &embededWM{}, icons)
	desk.run = desk.runEmbed
	desk.showMenu = desk.showMenuEmbed

	desk.root = desk.newDesktopWindowEmbed()
	desk.root.SetContent(desk.createPrimaryContent())
	return desk
}

func newDesktop(app fyne.App, wm fynedesk.WindowManager, icons fynedesk.ApplicationProvider) *desktop {
	desk := &desktop{app: app, wm: wm, icons: icons, screens: newEmbeddedScreensProvider()}
	desk.showMenu = desk.showMenuFull

	fynedesk.SetInstance(desk)
	desk.settings = newDeskSettings()
	desk.addSettingsChangeListener()

	desk.registerShortcuts()
	return desk
}

func (l *desktop) calculator() {
	err := exec.Command("calculator").Start()
	if err != nil {
		fyne.LogError("Failed to open calculator", err)
	}
}
