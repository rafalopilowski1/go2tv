// Command locale-smoke verifies that Go2TV follows the operating system locale
// automatically: it registers every committed catalog the same way the app does,
// then asserts the translations the app would show. It is a throwaway CI probe,
// kept out of the module's normal package tree.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	fyne "github.com/alexballas/refyne/v2"
	"github.com/alexballas/refyne/v2/lang"
	"github.com/jeandeaual/go-locale"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

const devicesKey = "%d devices found"

// supported mirrors radioDisplayLanguage in internal/gui/radio_country.go.
var supported = []language.Tag{
	language.English,
	language.SimplifiedChinese,
	language.TraditionalChinese,
	language.Polish,
}

type catalog struct {
	file     string
	tag      language.Tag
	messages map[string]string
}

type verifier struct {
	failures int
}

func (v *verifier) eq(label, got, want string) {
	if got == want {
		fmt.Printf("PASS %-24s %q\n", label, got)
		return
	}
	v.failures++
	fmt.Printf("FAIL %-24s got %q, want %q\n", label, got, want)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\nFAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("\nPASS: automatic OS locale matching verified")
}

func run() error {
	expectLocale := strings.TrimSpace(os.Getenv("GO2TV_SMOKE_EXPECT_LOCALE"))
	expectSettings := os.Getenv("GO2TV_SMOKE_EXPECT_SETTINGS")
	expectCountry := os.Getenv("GO2TV_SMOKE_EXPECT_COUNTRY_DE")
	for _, required := range []struct{ name, value string }{
		{"GO2TV_SMOKE_EXPECT_LOCALE", expectLocale},
		{"GO2TV_SMOKE_EXPECT_SETTINGS", expectSettings},
		{"GO2TV_SMOKE_EXPECT_COUNTRY_DE", expectCountry},
	} {
		if required.value == "" {
			return fmt.Errorf("%s must be set", required.name)
		}
	}

	dir := os.Getenv("GO2TV_SMOKE_TRANSLATIONS_DIR")
	if dir == "" {
		dir = filepath.Join("internal", "gui", "translations")
	}
	catalogs, err := loadCatalogs(dir)
	if err != nil {
		return err
	}

	fmt.Println("== catalogs (registered like AddTranslationsFS) ==")
	for _, c := range catalogs {
		fmt.Printf("%-14s -> %-8s %d messages\n", c.file, c.tag, len(c.messages))
	}

	osLocale, osLocaleErr := locale.GetLocale()
	osLocales, osLocalesErr := locale.GetLocales()
	systemLocale := lang.SystemLocale()

	fmt.Println("== OS locale ==")
	fmt.Printf("GOOS: %s\n", runtime.GOOS)
	fmt.Printf("go-locale GetLocale:  %q (err=%v)\n", osLocale, osLocaleErr)
	fmt.Printf("go-locale GetLocales: %q (err=%v)\n", osLocales, osLocalesErr)
	fmt.Printf("env LANG=%q LC_ALL=%q LC_MESSAGES=%q LANGUAGE=%q\n",
		os.Getenv("LANG"), os.Getenv("LC_ALL"), os.Getenv("LC_MESSAGES"), os.Getenv("LANGUAGE"))
	fmt.Printf("lang.SystemLocale():  %q\n", string(systemLocale))
	fmt.Printf("expect locale:        %q\n", expectLocale)

	v := &verifier{}
	requested := language.Make(expectLocale)
	system := language.Make(string(systemLocale))
	v.eq("SystemLocale base", base(system), base(requested))

	matched, index, confidence := language.NewMatcher(supported).Match(system)
	fmt.Printf("== matched UI language ==\n%s (index %d, confidence %v)\n", matched, index, confidence)
	v.eq("matcher base", base(matched), base(requested))

	region, err := language.ParseRegion("DE")
	if err != nil {
		return fmt.Errorf("parse region DE: %w", err)
	}
	v.eq("DE region name", display.Regions(matched).Name(region), expectCountry)
	v.eq("L(Settings)", lang.L("Settings"), expectSettings)
	fmt.Printf("INFO L(Devices): %q\n", lang.L("Devices"))

	// The catalogs use one count-neutral form for every device count, so the
	// same translated format string must format 2, 5 and 22.
	catalogFormat := ""
	for _, c := range catalogs {
		if c.tag == requested {
			catalogFormat = c.messages[devicesKey]
			break
		}
		if catalogFormat == "" && base(c.tag) == base(requested) {
			catalogFormat = c.messages[devicesKey]
		}
	}
	if catalogFormat == "" {
		return fmt.Errorf("catalog for %s has no %q entry", requested, devicesKey)
	}
	wantFormat := catalogFormat
	if envFormat := os.Getenv("GO2TV_SMOKE_EXPECT_DEVICES_FORMAT"); envFormat != "" {
		v.eq("catalog devices format", catalogFormat, envFormat)
		wantFormat = envFormat
	}
	actualFormat := lang.L(devicesKey)
	v.eq("L("+devicesKey+")", actualFormat, wantFormat)
	for _, count := range []int{2, 5, 22} {
		v.eq(fmt.Sprintf("devices n=%d", count),
			fmt.Sprintf(actualFormat, count), fmt.Sprintf(wantFormat, count))
	}

	if v.failures > 0 {
		return fmt.Errorf("%d check(s) failed", v.failures)
	}
	return nil
}

// base returns the canonical language subtag, ignoring script and region.
func base(t language.Tag) string {
	b, _ := t.Base()
	return b.String()
}

// loadCatalogs registers every catalog in dir exactly as AddTranslationsFS does:
// go-i18n infers the tag from the file name, so underscores are normalized to
// hyphens before the locale is handed to AddTranslationsForLocale.
func loadCatalogs(dir string) ([]catalog, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read catalogs: %w", err)
	}

	var catalogs []catalog
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		var messages map[string]string
		if err := json.Unmarshal(raw, &messages); err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		tag := language.Make(strings.ReplaceAll(strings.TrimSuffix(name, ".json"), "_", "-"))
		if err := lang.AddTranslationsForLocale(raw, fyne.Locale(tag.String())); err != nil {
			return nil, fmt.Errorf("register %s: %w", name, err)
		}
		catalogs = append(catalogs, catalog{file: name, tag: tag, messages: messages})
	}
	if len(catalogs) == 0 {
		return nil, fmt.Errorf("no catalogs found in %s", dir)
	}
	return catalogs, nil
}
