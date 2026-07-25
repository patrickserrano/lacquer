// Generates site/src/content/docs/skills/** from the actual SKILL.md sources
// in core/skills/ and profiles/<profile>/skills/ — the lacquer repo's real
// source of truth. Run before every `astro build`/`astro dev` (wired into
// package.json) so the site can never drift from what actually ships.
import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = join(__dirname, '..', '..');
const OUT_DIR = join(__dirname, '..', 'src', 'content', 'docs', 'skills');
const REPO_BLOB = 'https://github.com/patrickserrano/lacquer/blob/main';

function listDirs(path) {
	if (!existsSync(path)) return [];
	return readdirSync(path).filter((name) => statSync(join(path, name)).isDirectory());
}

function titleCase(slug) {
	return slug
		.split('-')
		.map((w) => w.charAt(0).toUpperCase() + w.slice(1))
		.join(' ');
}

// Every SKILL.md's frontmatter is hand-authored to a narrow, known shape (see
// skill-authoring-standard) — just `name:` and `description:` as either a
// bare/quoted single-line scalar or a `>`-folded block scalar. That's narrow
// enough to parse directly rather than pull in a YAML library (which caused a
// real conflict: Astro's own dependency tree pins an older js-yaml with a
// different export shape than current js-yaml, and hoisting a second copy
// broke Astro's internal prerendering).
function parseFrontmatterField(fmText, field) {
	const singleLine = fmText.match(new RegExp(`^${field}:[ \\t]*(.*)$`, 'm'));
	if (!singleLine) return '';
	const rest = singleLine[1].trim();
	if (/^[|>][+-]?$/.test(rest)) {
		// Block scalar: collect subsequent indented lines until dedent.
		const after = fmText.slice(fmText.indexOf(singleLine[0]) + singleLine[0].length);
		const collected = [];
		for (const line of after.split('\n')) {
			if (line.trim() === '') {
				collected.push('');
				continue;
			}
			if (/^\s+\S/.test(line)) collected.push(line.trim());
			else break;
		}
		return collected.join(' ').replace(/\s+/g, ' ').trim();
	}
	if ((rest.startsWith('"') && rest.endsWith('"')) || (rest.startsWith("'") && rest.endsWith("'"))) {
		return rest
			.slice(1, -1)
			.replace(/\\"/g, '"')
			.replace(/''/g, "'");
	}
	return rest;
}

function splitFrontmatter(raw) {
	const match = raw.match(/^---\r?\n([\s\S]*?)\r?\n---\r?\n?([\s\S]*)$/);
	if (!match) return { name: '', description: '', body: raw };
	return {
		name: parseFrontmatterField(match[1], 'name'),
		description: parseFrontmatterField(match[1], 'description'),
		body: match[2],
	};
}

// A JSON string is also a valid YAML double-quoted scalar (same escape
// rules), so this is a safe, dependency-free way to emit arbitrary title/
// description text without hand-rolling YAML escaping.
function writeDoc(outPath, { title, description }, body) {
	mkdirSync(dirname(outPath), { recursive: true });
	const frontmatter = `title: ${JSON.stringify(title)}\ndescription: ${JSON.stringify(description)}`;
	writeFileSync(outPath, `---\n${frontmatter}\n---\n\n${body.trimEnd()}\n`);
}

function syncedNote(sourcePath) {
	return (
		`:::note\n` +
		`Generated from [\`${sourcePath}\`](${REPO_BLOB}/${sourcePath}) — edit that file, not this page.\n` +
		`:::\n`
	);
}

// One skill directory (core/skills/<name> or profiles/<profile>/skills/<name>).
function generateSkill(group, skillsDir, name) {
	const skillDir = join(skillsDir, name);
	const skillMdPath = join(skillDir, 'SKILL.md');
	if (!existsSync(skillMdPath)) return;

	const sourcePrefix = group === 'core' ? `core/skills/${name}` : `profiles/${group}/skills/${name}`;
	const parsed = splitFrontmatter(readFileSync(skillMdPath, 'utf8'));
	const title = parsed.name || name;
	const description = parsed.description || `The ${title} skill.`;
	const { body } = parsed;

	const refsDir = join(skillDir, 'references');
	const refFiles = existsSync(refsDir) ? readdirSync(refsDir).filter((f) => f.endsWith('.md')).sort() : [];

	let fullBody = `${syncedNote(`${sourcePrefix}/SKILL.md`)}\n${body}`;

	if (refFiles.length > 0) {
		const refLines = refFiles.map((f) => {
			const slug = f.replace(/\.md$/, '');
			const refBody = readFileSync(join(refsDir, f), 'utf8');
			const headingMatch = refBody.match(/^#\s+(.+)$/m);
			const refTitle = headingMatch ? headingMatch[1].trim() : titleCase(slug);
			writeDoc(
				join(OUT_DIR, group, name, `${slug}.md`),
				{ title: refTitle, description: `Reference for the ${title} skill.` },
				`${syncedNote(`${sourcePrefix}/references/${f}`)}\n${refBody}`
			);
			return `- [${refTitle}](/lacquer/skills/${group}/${name}/${slug}/)`;
		});
		fullBody += `\n\n## Reference files\n\n${refLines.join('\n')}\n`;
	}

	writeDoc(join(OUT_DIR, group, `${name}.md`), { title, description }, fullBody);
}

function generateGroup(group, skillsDir) {
	for (const name of listDirs(skillsDir)) {
		generateSkill(group, skillsDir, name);
	}
}

rmSync(OUT_DIR, { recursive: true, force: true });
mkdirSync(OUT_DIR, { recursive: true });

generateGroup('core', join(REPO_ROOT, 'core', 'skills'));
for (const profile of listDirs(join(REPO_ROOT, 'profiles'))) {
	const skillsDir = join(REPO_ROOT, 'profiles', profile, 'skills');
	if (existsSync(skillsDir)) generateGroup(profile, skillsDir);
}

function countMd(dir) {
	let count = 0;
	for (const entry of readdirSync(dir)) {
		const full = join(dir, entry);
		if (statSync(full).isDirectory()) count += countMd(full);
		else if (entry.endsWith('.md')) count += 1;
	}
	return count;
}
console.log(`generate-skill-docs: wrote ${countMd(OUT_DIR)} skill pages into ${OUT_DIR}`);
