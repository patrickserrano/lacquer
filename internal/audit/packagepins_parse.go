package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Requirement kinds, in the spelling Xcode writes into a pbxproj. XcodeGen's and
// Package.swift's spellings are translated into these.
const (
	ReqExact         = "exactVersion"
	ReqUpToNextMajor = "upToNextMajorVersion"
	ReqUpToNextMinor = "upToNextMinorVersion"
	ReqRange         = "versionRange"
	ReqBranch        = "branch"
	ReqRevision      = "revision"
)

// PackageRequirement is one declared dependency on a remote Swift package.
type PackageRequirement struct {
	// URL is the repository URL as declared.
	URL string
	// Kind is one of the Req* constants, or whatever unknown kind the source
	// named (which the comparator then declines to judge).
	Kind string
	// Version is the exact version for ReqExact, and the lower bound for the
	// other version kinds.
	Version string
	// Max is ReqRange's upper bound, exclusive.
	Max string
	// Branch and Revision are the ReqBranch / ReqRevision values.
	Branch, Revision string
	// File and Line locate the declaration. Parsers set Line; the caller sets
	// File, since only it knows the repo-relative path.
	File string
	Line int
}

// uncheckedDecl is a dependency declaration the audit found and could not read
// as a requirement: a computed version, a registry package, an argument form it
// does not know. It is reported as such rather than guessed at.
type uncheckedDecl struct {
	File string
	Line int
	Why  string
}

// pinState is what a Package.resolved pin is resolved to.
type pinState struct {
	Version, Branch, Revision string
}

// resolvedPin is one entry of a Package.resolved.
type resolvedPin struct {
	Identity string
	Location string
	State    pinState
	// Line is where the pin's resolved state is spelled (its version, else its
	// branch, else its revision), so a report points at the value that is wrong.
	Line int
}

// --- semver -----------------------------------------------------------------

type semver struct {
	major, minor, patch uint64
	pre                 []string
}

// parseSemver parses a strict semantic version: MAJOR.MINOR.PATCH, optional
// -prerelease and +build. Strict on purpose: a "5.0" or "v1.2.3" is not a
// version SwiftPM accepts in a requirement, and reading it as one would be the
// audit guessing.
func parseSemver(s string) (semver, bool) {
	var v semver
	if i := strings.IndexByte(s, '+'); i >= 0 {
		if !validIdents(s[i+1:], false) {
			return v, false
		}
		s = s[:i]
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		if !validIdents(s[i+1:], true) {
			return v, false
		}
		v.pre = strings.Split(s[i+1:], ".")
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	nums := make([]uint64, 3)
	for i, p := range parts {
		n, ok := numericIdent(p)
		if !ok {
			return v, false
		}
		nums[i] = n
	}
	v.major, v.minor, v.patch = nums[0], nums[1], nums[2]
	return v, true
}

// numericIdent parses a numeric identifier: digits only, no leading zero.
func numericIdent(p string) (uint64, bool) {
	if p == "" || (len(p) > 1 && p[0] == '0') {
		return 0, false
	}
	for _, r := range p {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(p, 10, 64)
	return n, err == nil
}

// validIdents checks a dot-separated prerelease or build identifier list.
func validIdents(s string, pre bool) bool {
	if s == "" {
		return false
	}
	for _, id := range strings.Split(s, ".") {
		if id == "" {
			return false
		}
		allDigits := true
		for _, r := range id {
			switch {
			case r >= '0' && r <= '9':
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-':
				allDigits = false
			default:
				return false
			}
		}
		if pre && allDigits && len(id) > 1 && id[0] == '0' {
			return false
		}
	}
	return true
}

// compareSemver orders by semver precedence (semver.org §11). Build metadata
// never reaches here: parseSemver drops it.
func compareSemver(a, b semver) int {
	for _, d := range [][2]uint64{{a.major, b.major}, {a.minor, b.minor}, {a.patch, b.patch}} {
		if d[0] != d[1] {
			if d[0] < d[1] {
				return -1
			}
			return 1
		}
	}
	// A version with a prerelease sorts below the same version without one.
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1
	case len(b.pre) == 0:
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		if c := compareIdent(a.pre[i], b.pre[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a.pre) < len(b.pre):
		return -1
	case len(a.pre) > len(b.pre):
		return 1
	}
	return 0
}

// compareIdent compares prerelease identifiers: numeric ones numerically and
// below alphanumeric ones, alphanumeric ones in ASCII order.
func compareIdent(a, b string) int {
	na, aNum := numericIdent(a)
	nb, bNum := numericIdent(b)
	switch {
	case aNum && bNum:
		if na != nb {
			if na < nb {
				return -1
			}
			return 1
		}
		return 0
	case aNum:
		return -1
	case bNum:
		return 1
	}
	return strings.Compare(a, b)
}

func (v semver) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
	if len(v.pre) > 0 {
		s += "-" + strings.Join(v.pre, ".")
	}
	return s
}

// rangeContains is SwiftPM's Range<Version>.contains(version:): [lo, hi), with
// one addition for prereleases. A prerelease is inside only when a bound itself
// names a prerelease, and never when it is a prerelease of a release upper
// bound — 10.0.0-beta.1 sorts below 10.0.0, and is still not "before 10".
func rangeContains(lo, hi, v semver) bool {
	if len(v.pre) > 0 {
		if len(lo.pre) == 0 && len(hi.pre) == 0 {
			return false
		}
		if len(hi.pre) == 0 && hi.major == v.major && hi.minor == v.minor && hi.patch == v.patch {
			return false
		}
	}
	return compareSemver(lo, v) <= 0 && compareSemver(v, hi) < 0
}

// bounds returns a version requirement's [lo, hi) range. ok is false when the
// requirement is not a version range or names a version that is not one.
func (r PackageRequirement) bounds() (lo, hi semver, ok bool) {
	lo, ok = parseSemver(r.Version)
	if !ok {
		return lo, hi, false
	}
	switch r.Kind {
	case ReqUpToNextMajor:
		return lo, semver{major: lo.major + 1}, true
	case ReqUpToNextMinor:
		return lo, semver{major: lo.major, minor: lo.minor + 1}, true
	case ReqRange:
		hi, ok = parseSemver(r.Max)
		return lo, hi, ok
	}
	return lo, hi, false
}

// satisfiedBy reports whether a resolved pin meets the requirement. checked is
// false when either side cannot be read — an unknown kind, a version that is not
// one, a pin with no state — and ok is then meaningless: the caller reports the
// pair as not checked, never as passing or failing.
func (r PackageRequirement) satisfiedBy(p pinState) (ok, checked bool) {
	if p.Version == "" && p.Branch == "" && p.Revision == "" {
		return false, false
	}
	switch r.Kind {
	case ReqBranch:
		if r.Branch == "" {
			return false, false
		}
		return p.Branch == r.Branch, true
	case ReqRevision:
		if r.Revision == "" || p.Revision == "" {
			return false, false
		}
		return strings.EqualFold(p.Revision, r.Revision), true
	case ReqExact, ReqUpToNextMajor, ReqUpToNextMinor, ReqRange:
		want, okReq := parseSemver(r.Version)
		if !okReq {
			return false, false
		}
		if p.Version == "" {
			// Pinned to a branch or bare revision: no version to be in range.
			return false, true
		}
		got, okPin := parseSemver(p.Version)
		if !okPin {
			return false, false
		}
		if r.Kind == ReqExact {
			return compareSemver(got, want) == 0, true
		}
		lo, hi, okRange := r.bounds()
		if !okRange {
			return false, false
		}
		return rangeContains(lo, hi, got), true
	}
	return false, false
}

// describe renders the requirement for a report: what it admits, not just its
// kind name.
func (r PackageRequirement) describe() string {
	switch r.Kind {
	case ReqExact:
		return "exactly " + r.Version
	case ReqUpToNextMajor, ReqUpToNextMinor:
		name := map[string]string{ReqUpToNextMajor: "major", ReqUpToNextMinor: "minor"}[r.Kind]
		if lo, hi, ok := r.bounds(); ok {
			return fmt.Sprintf("up to next %s from %s (>= %s, < %s)", name, r.Version, lo, hi)
		}
		return fmt.Sprintf("up to next %s from %s", name, r.Version)
	case ReqRange:
		return fmt.Sprintf(">= %s, < %s", r.Version, r.Max)
	case ReqBranch:
		return "branch " + r.Branch
	case ReqRevision:
		return "revision " + r.Revision
	}
	return fmt.Sprintf("%q %s", r.Kind, r.Version)
}

// sameRequirement reports whether two declarations ask for the same thing.
func sameRequirement(a, b PackageRequirement) bool {
	return a.Kind == b.Kind && a.Version == b.Version && a.Max == b.Max && a.Branch == b.Branch && a.Revision == b.Revision
}

// describe renders what a pin is resolved to.
func (p pinState) describe() string {
	switch {
	case p.Version != "":
		return p.Version
	case p.Branch != "":
		return "branch " + p.Branch + " @ " + shortRev(p.Revision)
	}
	return "revision " + shortRev(p.Revision)
}

func shortRev(r string) string {
	if len(r) > 7 {
		return r[:7]
	}
	return r
}

// --- package identity ---------------------------------------------------------

// normalizePackageURL reduces a repository URL to host/path so every spelling
// of one repository compares equal: case, scheme, user, an scp-style ssh form,
// and a trailing .git or slash are all dropped. The owner is kept — matching on
// the last path segment alone would pair evil/sentry-cocoa with
// getsentry/sentry-cocoa.
func normalizePackageURL(u string) string {
	s := strings.ToLower(strings.TrimSpace(u))
	scp := true
	if i := strings.Index(s, "://"); i >= 0 {
		s, scp = s[i+3:], false
	}
	host, rest := s, ""
	if i := strings.IndexByte(s, '/'); i >= 0 {
		host, rest = s[:i], s[i:]
	}
	if i := strings.LastIndexByte(host, '@'); i >= 0 {
		host = host[i+1:]
	}
	if i := strings.IndexByte(host, ':'); i >= 0 {
		if scp {
			// git@github.com:owner/repo — the colon separates host and path.
			host, rest = host[:i], "/"+host[i+1:]+rest
		} else {
			host = host[:i] // a port
		}
	}
	s = host + rest
	for {
		t := strings.TrimSuffix(strings.TrimRight(s, "/"), ".git")
		if t == s {
			return s
		}
		s = t
	}
}

// packageName is the name a report uses for a package: SwiftPM's identity, the
// last path component of its URL.
func packageName(url string) string {
	return path.Base(normalizePackageURL(url))
}

// --- Package.resolved ---------------------------------------------------------

type rawPinState struct {
	Branch   *string `json:"branch"`
	Revision *string `json:"revision"`
	Version  *string `json:"version"`
}

func (s rawPinState) state() pinState {
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	return pinState{Version: deref(s.Version), Branch: deref(s.Branch), Revision: deref(s.Revision)}
}

// parseResolved reads a Package.resolved in any of the formats SwiftPM has
// written: v1 (pins under "object", keyed by package/repositoryURL) and v2/v3
// (top-level pins keyed by identity/location; v3 adds originHash).
func parseResolved(body []byte) ([]resolvedPin, error) {
	var doc struct {
		Version int `json:"version"`
		Pins    []struct {
			Identity string      `json:"identity"`
			Location string      `json:"location"`
			State    rawPinState `json:"state"`
		} `json:"pins"`
		Object struct {
			Pins []struct {
				RepositoryURL string      `json:"repositoryURL"`
				State         rawPinState `json:"state"`
			} `json:"pins"`
		} `json:"object"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	var pins []resolvedPin
	switch doc.Version {
	case 1:
		for _, p := range doc.Object.Pins {
			pins = append(pins, resolvedPin{Identity: packageName(p.RepositoryURL), Location: p.RepositoryURL, State: p.State.state()})
		}
	case 2, 3:
		for _, p := range doc.Pins {
			id := strings.ToLower(p.Identity)
			if id == "" {
				id = packageName(p.Location)
			}
			pins = append(pins, resolvedPin{Identity: id, Location: p.Location, State: p.State.state()})
		}
	default:
		return nil, fmt.Errorf("unsupported Package.resolved format version %d", doc.Version)
	}
	locatePins(body, pins)
	return pins, nil
}

// locatePins sets each pin's Line. JSON decoding loses positions, so the pin is
// found again in the text: its location (either slash spelling — v1 files often
// escape them), then the first occurrence of its state value after it. Pins are
// in file order, so the search resumes where the last one ended.
func locatePins(body []byte, pins []resolvedPin) {
	from := 0
	for i := range pins {
		p := &pins[i]
		at := -1
		for _, loc := range []string{p.Location, strings.ReplaceAll(p.Location, "/", `\/`)} {
			if j := bytes.Index(body[from:], []byte(`"`+loc+`"`)); j >= 0 {
				at = from + j
				break
			}
		}
		if at < 0 {
			continue
		}
		value := p.State.Version
		if value == "" {
			value = p.State.Branch
		}
		if value == "" {
			value = p.State.Revision
		}
		pos := at
		if j := bytes.Index(body[at:], []byte(`"`+value+`"`)); j >= 0 && value != "" {
			pos = at + j
		}
		p.Line = bytes.Count(body[:pos], []byte("\n")) + 1
		from = pos
	}
}

// --- project.pbxproj ----------------------------------------------------------

// plist is a value in an old-style (NeXT) property list, which is what a
// project.pbxproj is.
type plist struct {
	str  string
	dict *plistDict
	arr  []plist
}

type plistDict struct {
	keys  []string
	vals  map[string]plist
	lines map[string]int
}

func (d *plistDict) get(key string) plist {
	if d == nil {
		return plist{}
	}
	return d.vals[key]
}

type plistParser struct {
	src  string
	pos  int
	line int
}

var errPlistEOF = errors.New("unexpected end of project.pbxproj")

// skip moves past whitespace and comments.
func (p *plistParser) skip() {
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case c == '\n':
			p.line++
			p.pos++
		case c == ' ' || c == '\t' || c == '\r':
			p.pos++
		case strings.HasPrefix(p.src[p.pos:], "//"):
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
		case strings.HasPrefix(p.src[p.pos:], "/*"):
			end := strings.Index(p.src[p.pos+2:], "*/")
			if end < 0 {
				p.pos = len(p.src)
				return
			}
			p.line += strings.Count(p.src[p.pos:p.pos+2+end], "\n")
			p.pos += end + 4
		default:
			return
		}
	}
}

// token returns the next string token (quoted or bare) and the line it is on.
func (p *plistParser) token() (string, int, error) {
	p.skip()
	if p.pos >= len(p.src) {
		return "", p.line, errPlistEOF
	}
	line := p.line
	if p.src[p.pos] == '"' {
		var b strings.Builder
		p.pos++
		for p.pos < len(p.src) {
			c := p.src[p.pos]
			switch c {
			case '"':
				p.pos++
				return b.String(), line, nil
			case '\\':
				if p.pos+1 < len(p.src) {
					p.pos++
					switch e := p.src[p.pos]; e {
					case 'n':
						b.WriteByte('\n')
					case 't':
						b.WriteByte('\t')
					default:
						b.WriteByte(e)
					}
				}
			case '\n':
				p.line++
				b.WriteByte(c)
			default:
				b.WriteByte(c)
			}
			p.pos++
		}
		return "", line, errPlistEOF
	}
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if strings.IndexByte(" \t\r\n{}()=;,\"", c) >= 0 || strings.HasPrefix(p.src[p.pos:], "/*") || strings.HasPrefix(p.src[p.pos:], "//") {
			break
		}
		p.pos++
	}
	if p.pos == start {
		return "", line, fmt.Errorf("project.pbxproj line %d: unexpected %q", line, p.src[p.pos])
	}
	return p.src[start:p.pos], line, nil
}

func (p *plistParser) expect(c byte) error {
	p.skip()
	if p.pos >= len(p.src) {
		return errPlistEOF
	}
	if p.src[p.pos] != c {
		return fmt.Errorf("project.pbxproj line %d: expected %q, found %q", p.line, c, p.src[p.pos])
	}
	p.pos++
	return nil
}

func (p *plistParser) peek() byte {
	p.skip()
	if p.pos >= len(p.src) {
		return 0
	}
	return p.src[p.pos]
}

func (p *plistParser) value() (plist, error) {
	switch p.peek() {
	case 0:
		return plist{}, errPlistEOF
	case '{':
		p.pos++
		d := &plistDict{vals: map[string]plist{}, lines: map[string]int{}}
		for {
			if p.peek() == '}' {
				p.pos++
				return plist{dict: d}, nil
			}
			key, line, err := p.token()
			if err != nil {
				return plist{}, err
			}
			if err := p.expect('='); err != nil {
				return plist{}, err
			}
			v, err := p.value()
			if err != nil {
				return plist{}, err
			}
			if err := p.expect(';'); err != nil {
				return plist{}, err
			}
			if _, dup := d.vals[key]; !dup {
				d.keys = append(d.keys, key)
			}
			d.vals[key], d.lines[key] = v, line
		}
	case '(':
		p.pos++
		var arr []plist
		for {
			if p.peek() == ')' {
				p.pos++
				return plist{arr: arr}, nil
			}
			v, err := p.value()
			if err != nil {
				return plist{}, err
			}
			arr = append(arr, v)
			if p.peek() == ',' {
				p.pos++
			}
		}
	}
	s, _, err := p.token()
	return plist{str: s}, err
}

// parsePbxprojPackages returns a project's remote package requirements and the
// relative paths of its local packages, both in file order.
func parsePbxprojPackages(src string) (remote []PackageRequirement, local []string, err error) {
	p := &plistParser{src: src, line: 1}
	root, err := p.value()
	if err != nil {
		return nil, nil, err
	}
	objects := root.dict.get("objects").dict
	if objects == nil {
		return nil, nil, errors.New("project.pbxproj has no objects")
	}
	for _, id := range objects.keys {
		obj := objects.vals[id].dict
		switch obj.get("isa").str {
		case "XCLocalSwiftPackageReference":
			local = append(local, obj.get("relativePath").str)
		case "XCRemoteSwiftPackageReference":
			req := obj.get("requirement").dict
			r := PackageRequirement{URL: obj.get("repositoryURL").str, Kind: req.get("kind").str, Line: obj.lines["requirement"]}
			switch r.Kind {
			case ReqExact:
				r.Version = req.get("version").str
			case ReqUpToNextMajor, ReqUpToNextMinor:
				r.Version = req.get("minimumVersion").str
			case ReqRange:
				r.Version, r.Max = req.get("minimumVersion").str, req.get("maximumVersion").str
			case ReqBranch:
				r.Branch = req.get("branch").str
			case ReqRevision:
				r.Revision = req.get("revision").str
			}
			if r.Line == 0 {
				r.Line = objects.lines[id]
			}
			remote = append(remote, r)
		}
	}
	return remote, local, nil
}

// --- XcodeGen project.yml -----------------------------------------------------

type xcodeGenSpec struct {
	// Name is the spec's `name:`, which names the .xcodeproj it generates.
	Name      string
	Remote    []PackageRequirement
	Local     []string
	Unchecked []uncheckedDecl
}

// parseXcodeGenPackages reads the `packages:` map of an XcodeGen spec. Only the
// spec itself is read: packages pulled in through `include:` are not followed.
func parseXcodeGenPackages(body []byte) (xcodeGenSpec, error) {
	var spec xcodeGenSpec
	var doc yaml.Node
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return spec, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return spec, nil
	}
	top := doc.Content[0]
	var pkgs *yaml.Node
	for i := 0; i+1 < len(top.Content); i += 2 {
		switch top.Content[i].Value {
		case "name":
			spec.Name = top.Content[i+1].Value
		case "packages":
			pkgs = top.Content[i+1]
		}
	}
	if pkgs == nil || pkgs.Kind != yaml.MappingNode {
		return spec, nil
	}
	for i := 0; i+1 < len(pkgs.Content); i += 2 {
		key, val := pkgs.Content[i], pkgs.Content[i+1]
		f := map[string]string{}
		if val.Kind == yaml.MappingNode {
			for j := 0; j+1 < len(val.Content); j += 2 {
				f[val.Content[j].Value] = val.Content[j+1].Value
			}
		}
		url := f["url"]
		if url == "" && f["github"] != "" {
			url = "https://github.com/" + f["github"]
		}
		if url == "" {
			if f["path"] != "" {
				spec.Local = append(spec.Local, f["path"])
			} else {
				spec.Unchecked = append(spec.Unchecked, uncheckedDecl{Line: key.Line, Why: "package " + key.Value + " names neither a url nor a path"})
			}
			continue
		}
		r := PackageRequirement{URL: url, Line: key.Line}
		switch {
		case f["exactVersion"] != "":
			r.Kind, r.Version = ReqExact, f["exactVersion"]
		case f["version"] != "":
			r.Kind, r.Version = ReqExact, f["version"]
		case f["from"] != "":
			r.Kind, r.Version = ReqUpToNextMajor, f["from"]
		case f["majorVersion"] != "":
			r.Kind, r.Version = ReqUpToNextMajor, f["majorVersion"]
		case f["minorVersion"] != "":
			r.Kind, r.Version = ReqUpToNextMinor, f["minorVersion"]
		case f["minVersion"] != "" && f["maxVersion"] != "":
			r.Kind, r.Version, r.Max = ReqRange, f["minVersion"], f["maxVersion"]
		case f["branch"] != "":
			r.Kind, r.Branch = ReqBranch, f["branch"]
		case f["revision"] != "":
			r.Kind, r.Revision = ReqRevision, f["revision"]
		default:
			spec.Unchecked = append(spec.Unchecked, uncheckedDecl{Line: key.Line, Why: "package " + key.Value + " has no version requirement this audit reads"})
			continue
		}
		spec.Remote = append(spec.Remote, r)
	}
	return spec, nil
}

// --- Package.swift ------------------------------------------------------------

type swiftManifest struct {
	Remote    []PackageRequirement
	Local     []string
	Unchecked []uncheckedDecl
}

// swiftCode returns src with every comment blanked to spaces (newlines kept, so
// offsets still map to lines). String literals are left intact.
func swiftCode(src string) []byte {
	out := []byte(src)
	blank := func(i int) {
		if out[i] != '\n' {
			out[i] = ' '
		}
	}
	for i := 0; i < len(src); {
		switch {
		case strings.HasPrefix(src[i:], "//"):
			for i < len(src) && src[i] != '\n' {
				blank(i)
				i++
			}
		case strings.HasPrefix(src[i:], "/*"):
			// Swift block comments nest.
			depth := 0
			for i < len(src) {
				if strings.HasPrefix(src[i:], "/*") {
					depth++
					blank(i)
					blank(i + 1)
					i += 2
					continue
				}
				if strings.HasPrefix(src[i:], "*/") {
					depth--
					blank(i)
					blank(i + 1)
					i += 2
					if depth == 0 {
						break
					}
					continue
				}
				blank(i)
				i++
			}
		case src[i] == '"':
			i = skipSwiftString(src, i)
		default:
			i++
		}
	}
	return out
}

// skipSwiftString returns the index just past the string literal starting at i.
func skipSwiftString(src string, i int) int {
	if strings.HasPrefix(src[i:], `"""`) {
		if end := strings.Index(src[i+3:], `"""`); end >= 0 {
			return i + 3 + end + 3
		}
		return len(src)
	}
	for j := i + 1; j < len(src); j++ {
		switch src[j] {
		case '\\':
			j++
		case '"', '\n':
			return j + 1
		}
	}
	return len(src)
}

var packageCall = regexp.MustCompile(`\.package\s*\(`)

// Argument forms, matched against the call's arguments with every space outside
// a string literal removed. Only literal strings are read: a version held in a
// variable or built by interpolation is reported not checked.
const swiftStr = `"([^"\\]*)"`

var (
	swiftURLArg   = regexp.MustCompile(`^(?:name:` + swiftStr + `,)?url:` + swiftStr + `,(.*?),?$`)
	swiftPathArg  = regexp.MustCompile(`^(?:name:` + swiftStr + `,)?path:` + swiftStr + `,?$`)
	swiftReqForms = []struct {
		re   *regexp.Regexp
		kind string
	}{
		{regexp.MustCompile(`^from:` + swiftStr + `$`), ReqUpToNextMajor},
		{regexp.MustCompile(`^\.upToNextMajor\(from:` + swiftStr + `\)$`), ReqUpToNextMajor},
		{regexp.MustCompile(`^\.upToNextMinor\(from:` + swiftStr + `\)$`), ReqUpToNextMinor},
		{regexp.MustCompile(`^exact:` + swiftStr + `$`), ReqExact},
		{regexp.MustCompile(`^\.exact\(` + swiftStr + `\)$`), ReqExact},
		{regexp.MustCompile(`^` + swiftStr + `\.\.<` + swiftStr + `$`), ReqRange},
		{regexp.MustCompile(`^` + swiftStr + `\.\.\.` + swiftStr + `$`), "closedRange"},
		{regexp.MustCompile(`^branch:` + swiftStr + `$`), ReqBranch},
		{regexp.MustCompile(`^\.branch\(` + swiftStr + `\)$`), ReqBranch},
		{regexp.MustCompile(`^revision:` + swiftStr + `$`), ReqRevision},
		{regexp.MustCompile(`^\.revision\(` + swiftStr + `\)$`), ReqRevision},
	}
)

// parsePackageSwift reads the `.package(...)` dependency declarations of a
// Package.swift, without evaluating it: literal forms only, in file order.
func parsePackageSwift(src string) swiftManifest {
	var m swiftManifest
	code := swiftCode(src)
	for _, loc := range packageCall.FindAllIndex(code, -1) {
		line := bytes.Count(code[:loc[0]], []byte("\n")) + 1
		args, ok := callArgs(code, loc[1])
		if !ok {
			m.Unchecked = append(m.Unchecked, uncheckedDecl{Line: line, Why: "unterminated .package( call"})
			continue
		}
		flat := squeeze(args)
		if g := swiftPathArg.FindStringSubmatch(flat); g != nil {
			m.Local = append(m.Local, g[2])
			continue
		}
		g := swiftURLArg.FindStringSubmatch(flat)
		if g == nil {
			why := "not a literal .package(url:…) form this audit reads"
			if strings.HasPrefix(flat, "id:") {
				why = "a registry dependency (id:), which Package.resolved pins by identity, not URL"
			}
			m.Unchecked = append(m.Unchecked, uncheckedDecl{Line: line, Why: why})
			continue
		}
		r, ok := swiftRequirement(g[2], g[3])
		if !ok {
			m.Unchecked = append(m.Unchecked, uncheckedDecl{Line: line, Why: "requirement is not a literal form this audit reads"})
			continue
		}
		r.Line = line
		m.Remote = append(m.Remote, r)
	}
	return m
}

func swiftRequirement(url, rest string) (PackageRequirement, bool) {
	for _, f := range swiftReqForms {
		g := f.re.FindStringSubmatch(rest)
		if g == nil {
			continue
		}
		r := PackageRequirement{URL: url, Kind: f.kind}
		switch f.kind {
		case ReqBranch:
			r.Branch = g[1]
		case ReqRevision:
			r.Revision = g[1]
		case ReqRange:
			r.Version, r.Max = g[1], g[2]
		case "closedRange":
			// SwiftPM turns a...b into a..<(b with its patch + 1).
			r.Kind, r.Version = ReqRange, g[1]
			if hi, ok := parseSemver(g[2]); ok {
				hi.patch++
				r.Max = hi.String()
			}
		default:
			r.Version = g[1]
		}
		return r, true
	}
	return PackageRequirement{}, false
}

// callArgs returns the text between the '(' ending at open and its matching ')'.
func callArgs(code []byte, open int) (string, bool) {
	src := string(code)
	depth := 1
	for i := open; i < len(src); {
		switch src[i] {
		case '"':
			i = skipSwiftString(src, i)
			continue
		case '(', '[':
			depth++
		case ')', ']':
			depth--
			if depth == 0 {
				return src[open:i], true
			}
		}
		i++
	}
	return "", false
}

// squeeze removes whitespace outside string literals.
func squeeze(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '"' {
			end := skipSwiftString(s, i)
			b.WriteString(s[i:end])
			i = end
			continue
		}
		if !strings.ContainsRune(" \t\r\n", rune(s[i])) {
			b.WriteByte(s[i])
		}
		i++
	}
	return b.String()
}
