package ui

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// variantTheme forces the default theme into light or dark mode.
type variantTheme struct {
	fyne.Theme
	variant fyne.ThemeVariant
}

func (t variantTheme) Color(n fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	return t.Theme.Color(n, t.variant)
}

func applyTheme(app fyne.App, name string) {
	switch name {
	case "Light":
		app.Settings().SetTheme(variantTheme{theme.DefaultTheme(), theme.VariantLight})
	case "Dark":
		app.Settings().SetTheme(variantTheme{theme.DefaultTheme(), theme.VariantDark})
	default:
		app.Settings().SetTheme(theme.DefaultTheme())
	}
}

func errorf(format string, args ...any) error { return fmt.Errorf(format, args...) }
