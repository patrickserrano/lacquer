# secret-placeholders.awk — print the keys of an xcconfig-format input whose
# values are still placeholders, space-separated on one line, or an empty line.
#
# The one definition of "still a placeholder", shared by write-release-config.sh
# (the file it is about to archive against) and verify-archived-secrets.sh (the
# values the archived app's Info.plist actually carries). Two copies would drift,
# and the check that drifted would be the one that quietly stopped catching.
#
# Input: `KEY = value` lines; anything else is skipped. A value carrying `//`
# must arrive escaped as `/$()/`, as an xcconfig holds it.
# Environment:
#   XCCONFIG_DECLARED  space-separated keys whose secret must not equal the
#                      template's value for that key
#   XCCONFIG_TEMPLATE  the committed `.example` template; a missing file means
#                      no template values (getline returns -1)
#
# Only KEY names are printed, never a value. awk exits 0 having judged the input;
# any other status is the check itself failing, which a caller must treat as a
# failure, never as "no placeholders".

# value is what xcconfig reads: cut at the `//` comment marker, trimmed,
# with the `/$()/` escape undone. whole skips the cut, because a template
# that wrote a URL placeholder unescaped holds the placeholder a human
# copies, not the `https:` xcconfig would read.
function whole(line) {
	sub(/^[^=]*=/, "", line)
	sub(/^[ \t]+/, "", line)
	sub(/[ \t\r]+$/, "", line)
	gsub(/\/\$\(\)\//, "//", line)
	return line
}
function value(line) {
	sub(/^[^=]*=/, "", line)
	sub(/\/\/.*/, "", line)
	return whole("=" line)
}
function key(line) {
	sub(/[ \t]*=.*/, "", line)
	return line
}
function assignment(line) {
	return line ~ /^[A-Za-z_][A-Za-z0-9_]*[ \t]*=/
}
BEGIN {
	n = split(ENVIRON["XCCONFIG_DECLARED"], d, " ")
	for (i = 1; i <= n; i++) declared[d[i]] = 1
	# A missing template makes getline return -1: no template values.
	tf = ENVIRON["XCCONFIG_TEMPLATE"]
	while ((getline line < tf) > 0)
		if (assignment(line)) {
			template[key(line)] = value(line)
			template_whole[key(line)] = whole(line)
		}
}
!assignment($0) { next }
{
	k = key($0); v = value($0)
	if (tolower(v) ~ /your[-_]|change[-_]?me|<[^>]*>/ ||
	    (k in declared && k in template && (v == template[k] || v == template_whole[k]))) {
		if (!(k in seen)) { seen[k] = 1; out = out (out == "" ? "" : " ") k }
	}
}
END { print out }
