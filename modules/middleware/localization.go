package middleware

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	core "github.com/Unknwon/i18n"
	"github.com/Unknwon/macaron"
	"github.com/macaron-contrib/i18n"
)

// Localization layers built-in English defaults below each configured locale
// and its custom overlay. The pinned middleware still owns request selection,
// language cookies, redirects, descriptions, and argument formatting.
// Compose this before serving requests, as required by the locale registry.
func Localization(opt i18n.Options) macaron.Handler {
	section := opt.Section
	if section == "" {
		section = "i18n"
	}
	config := macaron.Config().Section(section)
	if len(opt.Langs) == 0 {
		opt.Langs = config.Key("LANGS").Strings(",")
	}
	if len(opt.Names) == 0 {
		opt.Names = config.Key("NAMES").Strings(",")
	}
	if len(opt.Langs) == 0 || len(opt.Langs) != len(opt.Names) {
		panic("localization requires matching configured languages and names")
	}
	if opt.Directory == "" {
		opt.Directory = config.Key("DIRECTORY").MustString("conf/locale")
	}
	if opt.CustomDirectory == "" {
		opt.CustomDirectory = config.Key("CUSTOM_DIRECTORY").MustString("custom/conf/locale")
	}
	if opt.Format == "" {
		opt.Format = config.Key("FORMAT").MustString("locale_%s.ini")
	}
	english := filepath.Join(opt.Directory, fmt.Sprintf(opt.Format, "en-US"))
	for index, lang := range opt.Langs {
		// The underlying registry is process-wide and immutable once registered.
		// Repeated app/fixture composition must retain its first layered definition.
		if core.IsExist(lang) {
			continue
		}
		name := fmt.Sprintf(opt.Format, lang)
		sources := []interface{}{filepath.Join(opt.Directory, name)}
		custom := filepath.Join(opt.CustomDirectory, name)
		if info, err := os.Stat(custom); err == nil && !info.IsDir() {
			sources = append(sources, custom)
		} else if err != nil && !os.IsNotExist(err) {
			panic(fmt.Errorf("custom locale %s: %w", lang, err))
		}
		if err := core.SetMessageWithDesc(lang, opt.Names[index], english, sources...); err != nil {
			panic(fmt.Errorf("locale %s: %w", lang, err))
		}
	}
	selectLocale := i18n.I18n(opt)
	return func(ctx *macaron.Context) {
		// The pinned selector adds SubURL on redirects. Macaron already made
		// URL route-relative, while retaining the original RequestURI.
		original := ctx.Req.RequestURI
		routeURL := *ctx.Req.URL
		// Macaron trims Path alone; keep its escaped counterpart relative too.
		if routeURL.RawPath != "" {
			prefix := (&url.URL{Path: strings.TrimSuffix(opt.SubURL, "/")}).EscapedPath()
			routeURL.RawPath = strings.TrimPrefix(routeURL.RawPath, prefix)
		}
		ctx.Req.RequestURI = routeURL.RequestURI()
		defer func() { ctx.Req.RequestURI = original }()
		if _, err := ctx.Invoke(selectLocale); err != nil {
			panic(err)
		}
	}
}
