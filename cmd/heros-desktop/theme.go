package main

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// CelestialOpts configures the heros-desktop “celestial” palette.
type CelestialOpts struct {
	// Light selects a daytime (paper + ink) palette instead of the default night sky.
	Light bool
	// Accent, when non-nil, overrides primary and related focus/hover/selection tones.
	Accent *color.NRGBA
}

// ParseHexRGB parses #RGB, #RRGGBB, or RRGGBB into NRGBA (opaque). Empty or invalid returns ok=false.
func ParseHexRGB(s string) (c color.NRGBA, ok bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return c, false
	}
	s = strings.TrimPrefix(s, "#")
	if len(s) == 3 {
		s = fmt.Sprintf("%c%c%c%c%c%c", s[0], s[0], s[1], s[1], s[2], s[2])
	}
	if len(s) != 6 {
		return c, false
	}
	v64, err := strconv.ParseUint(s, 16, 24)
	if err != nil {
		return c, false
	}
	v := uint32(v64)
	return color.NRGBA{R: uint8(v >> 16), G: uint8((v >> 8) & 0xff), B: uint8(v & 0xff), A: 0xff}, true
}

// formatHexAccent returns a lowercase #rrggbb string for JSON prefs.
func formatHexAccent(c color.NRGBA) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// NewCelestialTheme builds a Fyne theme (dark “night sky” or light “daybreak”) with optional accent override.
func NewCelestialTheme(o CelestialOpts) fyne.Theme {
	base := theme.DarkTheme()
	if o.Light {
		base = theme.LightTheme()
	}
	return &celestialTheme{base: base, opts: o}
}

type celestialTheme struct {
	base fyne.Theme
	opts CelestialOpts
}

func (t *celestialTheme) primary() color.NRGBA {
	if t.opts.Accent != nil {
		return *t.opts.Accent
	}
	if t.opts.Light {
		return color.NRGBA{R: 0x5b, G: 0x4b, B: 0xd8, A: 0xff}
	}
	return color.NRGBA{R: 0x8b, G: 0x7a, B: 0xff, A: 0xff}
}

func (t *celestialTheme) Color(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	if t.opts.Light {
		return t.colorLight(n, v)
	}
	return t.colorDark(n, v)
}

func (t *celestialTheme) colorDark(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	p := t.primary()
	hyper := color.NRGBA{R: 0x7d, G: 0xdb, B: 0xf7, A: 0xff}
	if t.opts.Accent != nil {
		hyper = blendNRGBA(p, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, 0.25)
	}
	switch n {
	case theme.ColorNamePrimary:
		return p
	case theme.ColorNameHyperlink:
		return hyper
	case theme.ColorNameFocus:
		return blendNRGBA(p, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, 0.38)
	case theme.ColorNameSelection:
		return blendNRGBA(p, color.NRGBA{R: 0x12, G: 0x10, B: 0x1c, A: 0xff}, 0.55)
	case theme.ColorNameHover:
		return color.NRGBA{R: 0x3d, G: 0x38, B: 0x55, A: 0xff}
	case theme.ColorNamePressed:
		return color.NRGBA{R: 0x52, G: 0x4a, B: 0x7a, A: 0xff}
	case theme.ColorNameInputBorder:
		return blendNRGBA(p, color.NRGBA{R: 0x1a, G: 0x17, B: 0x2a, A: 0xff}, 0.35)
	case theme.ColorNameInputBackground:
		return color.NRGBA{R: 0x1a, G: 0x17, B: 0x2a, A: 0xff}
	case theme.ColorNameBackground:
		return color.NRGBA{R: 0x12, G: 0x10, B: 0x1c, A: 0xff}
	case theme.ColorNameMenuBackground:
		return color.NRGBA{R: 0x1c, G: 0x19, B: 0x2e, A: 0xff}
	case theme.ColorNameButton:
		return color.NRGBA{R: 0x2a, G: 0x26, B: 0x3d, A: 0xff}
	case theme.ColorNameForeground:
		return color.NRGBA{R: 0xf4, G: 0xf1, B: 0xff, A: 0xff}
	case theme.ColorNameDisabled:
		return color.NRGBA{R: 0x8a, G: 0x85, B: 0x9e, A: 0xff}
	case theme.ColorNameSuccess:
		return color.NRGBA{R: 0x4e, G: 0xd9, B: 0xb0, A: 0xff}
	case theme.ColorNameWarning:
		return color.NRGBA{R: 0xff, G: 0xc9, B: 0x6b, A: 0xff}
	case theme.ColorNameError:
		return color.NRGBA{R: 0xff, G: 0x7a, B: 0xa8, A: 0xff}
	case theme.ColorNameShadow:
		return color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0x55}
	default:
		return t.base.Color(n, v)
	}
}

func (t *celestialTheme) colorLight(n fyne.ThemeColorName, v fyne.ThemeVariant) color.Color {
	p := t.primary()
	switch n {
	case theme.ColorNamePrimary:
		return p
	case theme.ColorNameHyperlink:
		if t.opts.Accent != nil {
			return blendNRGBA(p, color.NRGBA{R: 0x00, G: 0x6e, B: 0xc9, A: 0xff}, 0.35)
		}
		return color.NRGBA{R: 0x25, G: 0x6c, B: 0xd4, A: 0xff}
	case theme.ColorNameFocus:
		return blendNRGBA(p, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, 0.45)
	case theme.ColorNameSelection:
		return blendNRGBA(p, color.NRGBA{R: 0xf6, G: 0xf4, B: 0xfc, A: 0xff}, 0.5)
	case theme.ColorNameHover:
		return blendNRGBA(p, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, 0.72)
	case theme.ColorNamePressed:
		return blendNRGBA(p, color.NRGBA{R: 0xd0, G: 0xcc, B: 0xe8, A: 0xff}, 0.55)
	case theme.ColorNameInputBorder:
		return blendNRGBA(p, color.NRGBA{R: 0xe8, G: 0xe4, B: 0xf4, A: 0xff}, 0.4)
	case theme.ColorNameInputBackground:
		return color.NRGBA{R: 0xff, G: 0xfe, B: 0xff, A: 0xff}
	case theme.ColorNameBackground:
		return color.NRGBA{R: 0xf6, G: 0xf4, B: 0xfc, A: 0xff}
	case theme.ColorNameMenuBackground:
		return color.NRGBA{R: 0xfc, G: 0xfb, B: 0xff, A: 0xff}
	case theme.ColorNameButton:
		return color.NRGBA{R: 0xec, G: 0xe8, B: 0xf9, A: 0xff}
	case theme.ColorNameForeground:
		return color.NRGBA{R: 0x22, G: 0x1f, B: 0x32, A: 0xff}
	case theme.ColorNameDisabled:
		return color.NRGBA{R: 0x8f, G: 0x8a, B: 0x9e, A: 0xff}
	case theme.ColorNameSuccess:
		return color.NRGBA{R: 0x0d, G: 0x8f, B: 0x6f, A: 0xff}
	case theme.ColorNameWarning:
		return color.NRGBA{R: 0xb4, G: 0x5c, B: 0x00, A: 0xff}
	case theme.ColorNameError:
		return color.NRGBA{R: 0xc4, G: 0x1e, B: 0x3a, A: 0xff}
	case theme.ColorNameShadow:
		return color.NRGBA{R: 0x2a, G: 0x24, B: 0x40, A: 0x18}
	default:
		return t.base.Color(n, v)
	}
}

func blendNRGBA(a, b color.NRGBA, t float32) color.NRGBA {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return color.NRGBA{
		R: uint8(float32(a.R)*(1-t) + float32(b.R)*t),
		G: uint8(float32(a.G)*(1-t) + float32(b.G)*t),
		B: uint8(float32(a.B)*(1-t) + float32(b.B)*t),
		A: uint8(float32(a.A)*(1-t) + float32(b.A)*t),
	}
}

func (t *celestialTheme) Icon(n fyne.ThemeIconName) fyne.Resource {
	return t.base.Icon(n)
}

func (t *celestialTheme) Font(s fyne.TextStyle) fyne.Resource {
	return t.base.Font(s)
}

func (t *celestialTheme) Size(n fyne.ThemeSizeName) float32 {
	switch n {
	case theme.SizeNameText:
		return t.base.Size(n) + 1
	case theme.SizeNameInnerPadding:
		return t.base.Size(n) + 2
	case theme.SizeNamePadding:
		return t.base.Size(n) + 2
	default:
		return t.base.Size(n)
	}
}
