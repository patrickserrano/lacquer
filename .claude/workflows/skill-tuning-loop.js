export const meta = {
  name: 'skill-tuning-loop',
  description: 'For each skill: mine sessions for friction (or audit against skill-authoring-standard when evidence is thin), propose a bounded edit, validate against held-out cases before it ships',
  phases: [
    { title: 'Mine', detail: 'read the skill (Haiku), search recorded sessions for friction (Sonnet)', model: 'claude-sonnet-5' },
    { title: 'Reflect', detail: 'identify recurring patterns, or audit directly when evidence is thin', model: 'claude-haiku-4-5' },
    { title: 'Propose', detail: 'draft a bounded edit addressing the patterns (never written to disk)' },
    { title: 'Validate', detail: 'run old vs new skill against held-out tasks, judge the result (never written to disk)' },
  ],
}

const REFLECTION_SCHEMA = {
  type: 'object',
  properties: {
    patterns: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          issue: { type: 'string' },
          source: { type: 'string', enum: ['session-evidence', 'authoring-audit'] },
          sessionCount: { type: 'number' },
          evidence: { type: 'string' },
        },
        required: ['issue', 'source', 'evidence'],
      },
    },
  },
  required: ['patterns'],
}

const PROPOSAL_SCHEMA = {
  type: 'object',
  properties: {
    newSkillContent: { type: 'string' },
    rationale: { type: 'string' },
  },
  required: ['newSkillContent', 'rationale'],
}

const VERDICT_SCHEMA = {
  type: 'object',
  properties: {
    verdict: { type: 'string', enum: ['ACCEPT', 'REJECT'] },
    reasoning: { type: 'string' },
  },
  required: ['verdict', 'reasoning'],
}

// Cheap stages (reading a file, Reflect's pattern extraction) run on Haiku —
// no dynamic tool discovery involved, plain judgment over text already in
// context. The one exception is mine-sessions below: the prior run showed
// Haiku at low effort reliably fails the "discover mcp__agentsview__* via
// ToolSearch, then actually invoke it" pattern — 23 of 33 skills' Mine
// agents gave up on the search entirely, talking themselves into believing
// MCP tools "can't be invoked" through their function-calling interface.
// That's a tool-use reliability gap at this model/effort tier, not a prompt
// problem, so mine-sessions gets a stronger model instead of a stronger
// prompt. Propose/Validate inherit the session model regardless, since a bad
// skill edit or a sloppy judge call is exactly what the validation gate
// exists to catch — except the final judge verdict below, which forces a
// stronger model deliberately: it's the one load-bearing moment that decides
// ACCEPT/REJECT, the same posture as advisor-checkpoint (spend the expensive
// model at the few moments that matter), and judging with a different,
// stronger model than the one that drafted the proposal avoids the
// self-preferencing bias a same-tier judge would risk.
const CHEAP_MODEL = 'claude-haiku-4-5'
const MINE_SEARCH_MODEL = 'claude-sonnet-5'
const JUDGE_MODEL = 'claude-opus-5'
const BUDGET_FLOOR = 50_000 // stop starting new expensive stages below this much remaining

// Every agent() call in this workflow only ever needs to read and reason —
// nothing here is supposed to touch disk. A prior run gave Propose/Validate
// agents the default subagent (full Edit/Write access), and despite being
// told to "return" content, they wrote real SKILL.md files directly,
// rejected proposals included, plus stray edits to unrelated docs. Round 1
// of the fix used the built-in Explore agent (no Edit/Write/NotebookEdit),
// which stopped the file-mutation bug — but Explore still has Bash, and one
// rollout stage used it to compile a Swift binary while "describing" a
// held-out task instead of just describing it.
//
// core/agents/skill-tuning-reader.md (Read/Grep/Glob/WebFetch/WebSearch
// only, no Bash at all) closes that gap completely, but Claude Code only
// loads the subagent registry at session start — a custom agent created
// mid-session isn't available until a restart. Staying on Explore for now;
// switch READ_ONLY to { agentType: 'skill-tuning-reader' } after a session
// restart for the stronger guarantee (no code change needed beyond this
// line). The tightened NO_DISK_WRITES prompt below is the real defense
// under Explore in the meantime.
const READ_ONLY = { agentType: 'Explore' }
const NO_DISK_WRITES =
  'Do not use Edit, Write, NotebookEdit, or any Bash command that modifies a file, and do not compile, ' +
  'build, run, or execute any code (no `swiftc`, `go build`, `npm run`, `xcodebuild`, test runners, etc.) ' +
  '— this call must not leave any trace on the filesystem, and must not produce a binary, log, or build ' +
  'artifact. If a task seems to need running something to answer well, describe what you expect based on ' +
  'reading the code instead. Your only output is the structured value you return.'

// A real run of this workflow proposed "fixes" to manager-loop, github-ci-fix,
// and ios-debugger-agent that all rested on the same false premise: a tool or
// skill "doesn't exist," verified only by grepping this repo. Grep can't see
// Claude Code's own system-level tools (ScheduleWakeup, EnterPlanMode,
// CronCreate, TaskCreate, ...) or plugin-provided skills installed outside
// core/skills//profiles/*/skills/ (flowdeck, swift-concurrency-pro, ...) — all
// three of those were real, and the "fixes" made the skills worse, not better.
// This is a hard constraint, not a style note: repo-grep is not evidence of
// non-existence, full stop.
const NO_UNVERIFIABLE_EXISTENCE_CLAIMS =
  'Do not base a finding or edit on a claim that a tool, skill, or command "does not exist," "is undefined," ' +
  '"is not available," or similar — you cannot verify that from inside this workflow. Grepping this repo only ' +
  'sees files committed here; it cannot see Claude Code\'s own system-level tools (e.g. ScheduleWakeup, ' +
  'EnterPlanMode, CronCreate, TaskCreate) or plugin-provided skills installed outside this repo\'s own ' +
  'core/skills//profiles/*/skills/ directories (e.g. flowdeck, swift-concurrency-pro). If a skill references a ' +
  'tool or skill you can\'t find in this repo, that is not evidence it\'s missing — leave that reference alone ' +
  'rather than editing it away or flagging it as a defect.'

function skipBudget(prev) {
  return budget.total && budget.remaining() < BUDGET_FLOOR ? { ...prev, skip: true, reason: 'budget' } : null
}

// args can arrive as a parsed object or (depending on how the caller passed it)
// a JSON-encoded string — normalize defensively rather than assume either.
const parsedArgs = typeof args === 'string' ? JSON.parse(args) : args
const skillList = Array.isArray(parsedArgs) ? parsedArgs : parsedArgs.skills
if (!Array.isArray(skillList)) {
  throw new Error(`Expected args.skills to be an array, got: ${JSON.stringify(parsedArgs).slice(0, 200)}`)
}

const results = await pipeline(
  skillList, // [{ name, path }, ...] — path is the directory containing SKILL.md, e.g. "core/skills/foo"

  // --- Mine ---
  async (_, item) => {
    const skillPath = `${item.path}/SKILL.md`
    const currentSkill = await agent(
      `Read ${skillPath} and return its full contents verbatim. ${NO_DISK_WRITES}`,
      { label: `${item.name}:read`, phase: 'Mine', model: CHEAP_MODEL, effort: 'low', ...READ_ONLY }
    )
    const evidence = await agent(
      `Search recorded agent sessions for real friction tied to the "${item.name}" skill. Do this in two passes, ` +
      `not one combined query — a single query ANDing the skill name with friction phrases will under-match, ` +
      `since a session rarely contains both in the same message:\n` +
      `1. Use mcp__agentsview__search_sessions with the query "${item.name}" alone (no other terms) to find up ` +
      `to 15 recent sessions where this skill plausibly fired.\n` +
      `2. For each plausible hit, call get_session_overview then get_messages around the match, and read the ` +
      `surrounding conversation for friction: corrections ("no that's wrong", "actually", "don't", "that's not ` +
      `right"), retries, or the user contradicting what the skill told the agent to do.\n` +
      `Return, per session: session_id and either a short excerpt of what went wrong or "no friction found". If ` +
      `nothing plausible turns up in pass 1, say so plainly — do not force a match. These MCP tools were loaded ` +
      `via ToolSearch and are invoked exactly like any other tool call — do not conclude they "can't be invoked" ` +
      `through your function-calling interface; if a call fails, retry once before reporting a real failure. ` +
      `${NO_DISK_WRITES}`,
      { label: `${item.name}:mine`, phase: 'Mine', model: MINE_SEARCH_MODEL, effort: 'medium', ...READ_ONLY }
    )
    return { name: item.name, path: item.path, currentSkill, evidence }
  },

  // --- Reflect ---
  async (prev, item) => {
    const reflection = await agent(
      `Skill "${item.name}" (this is the ONLY text that counts as "the skill definition" below):\n\n` +
      `<<<SKILL>>>\n${prev.currentSkill}\n<<<END SKILL>>>\n\n` +
      `Evidence gathered from real sessions (a raw research note ABOUT the skill, not part of the skill file — ` +
      `if this note is malformed, repetitive, or self-contradictory, that is a flaw in the note, never something ` +
      `to report as an issue "in the skill definition"):\n\n` +
      `<<<EVIDENCE>>>\n${prev.evidence}\n<<<END EVIDENCE>>>\n\n` +
      `First: name concrete, RECURRING failure patterns from the session evidence — require independent evidence ` +
      `from 2+ sessions per pattern, tag them source: "session-evidence". State what the SKILL text (between the ` +
      `SKILL markers) currently says or omits, and what went wrong because of it.\n\n` +
      `If the session evidence above is thin or absent ("no friction found" for every session, or no sessions at ` +
      `all), don't stop there — instead directly audit the SKILL text against this repo's skill-authoring-standard: ` +
      `tight trigger-oriented description (states WHEN to use it, not just what it is), single responsibility (no ` +
      `"and also" sections), instruction over exposition (cut throat-clearing, keep concrete/checkable steps), ` +
      `companion files only when they earn their keep, name matches its directory. Tag findings from this pass ` +
      `source: "authoring-audit". Only surface real, specific defects that are actually present in the SKILL text — ` +
      `not manufactured nitpicks, and never a defect you only observed in the evidence note. If the skill ` +
      `genuinely has neither kind of issue, return an empty patterns array. ${NO_UNVERIFIABLE_EXISTENCE_CLAIMS} ${NO_DISK_WRITES}`,
      { label: `${item.name}:reflect`, phase: 'Reflect', schema: REFLECTION_SCHEMA, model: CHEAP_MODEL, effort: 'medium', ...READ_ONLY }
    )
    if (!reflection.patterns.length) {
      log(`${item.name}: no issues found (evidence or audit) — skipping.`)
      return { ...prev, reflection, skip: true, reason: 'no-issues' }
    }
    return { ...prev, reflection }
  },

  // --- Propose ---
  async (prev, item) => {
    if (prev.skip) return prev
    const budgetSkip = skipBudget(prev)
    if (budgetSkip) {
      log(`${item.name}: budget nearly exhausted (${Math.round(budget.remaining() / 1000)}k left) — skipping Propose/Validate.`)
      return budgetSkip
    }
    const proposal = await agent(
      `Propose a MINIMAL edit to the "${item.name}" skill addressing: ${JSON.stringify(prev.reflection.patterns)}\n\n` +
      `Current skill:\n${prev.currentSkill}\n\n` +
      `Bounded edit only — touch the lines implicated by the findings, preserve everything else. Follow this repo's ` +
      `skill-authoring-standard (trigger-oriented description, instruction over exposition, no padding). If a ` +
      `finding itself rests on a tool/skill "doesn't exist" claim, do not implement that finding — drop it and ` +
      `address only the findings that don't depend on it, or return the skill unchanged if none remain. ` +
      `Return the full new SKILL.md content as a STRING VALUE in your structured output — this is a draft for a ` +
      `human to review later, not something to apply now. ${NO_UNVERIFIABLE_EXISTENCE_CLAIMS} ${NO_DISK_WRITES}`,
      { label: `${item.name}:propose`, phase: 'Propose', schema: PROPOSAL_SCHEMA, ...READ_ONLY }
    )
    return { ...prev, proposal }
  },

  // --- Validate ---
  async (prev, item) => {
    if (prev.skip) return prev
    const evalCases = await agent(
      `Read ${item.path}/references/eval-cases.md if it exists; else propose 3 representative held-out tasks the ` +
      `"${item.name}" skill should handle well, derived from its description below. For each task, state an ` +
      `explicit, checkable success criterion (not "handles it well" — the specific behavior that counts as a ` +
      `pass) and make at least one of the three an edge/ambiguous case (a boundary condition, an input the ` +
      `description doesn't obviously cover, or a case where the skill should recognize it does NOT apply). ` +
      `${NO_DISK_WRITES}\n\n${prev.currentSkill}`,
      { label: `${item.name}:eval-cases`, phase: 'Validate', model: CHEAP_MODEL, effort: 'low', ...READ_ONLY }
    )
    const [before, after] = await parallel([
      () => agent(
        `Using ONLY this skill text as guidance, DESCRIBE IN DETAIL what you would do for each of these tasks — ` +
        `do not actually execute them or touch any file. ${NO_DISK_WRITES}\n\nTasks:\n${evalCases}\n\nSkill:\n${prev.currentSkill}`,
        { label: `${item.name}:rollout-old`, phase: 'Validate', ...READ_ONLY }
      ),
      () => agent(
        `Using ONLY this skill text as guidance, DESCRIBE IN DETAIL what you would do for each of these tasks — ` +
        `do not actually execute them or touch any file. ${NO_DISK_WRITES}\n\nTasks:\n${evalCases}\n\nSkill:\n${prev.proposal.newSkillContent}`,
        { label: `${item.name}:rollout-new`, phase: 'Validate', ...READ_ONLY }
      ),
    ])
    const judged = await agent(
      `Score both rollout descriptions against the same held-out tasks for the "${item.name}" skill.\n` +
      `Old:\n${before}\n\nNew:\n${after}\n\nProposal rationale, for context on what changed and why:\n${prev.proposal.rationale}\n\n` +
      `Does the new version avoid regressing every case and strictly improve at least one? REJECT regardless of ` +
      `rollout quality if the rationale's justification for a change rests on a tool/skill "doesn't exist" claim — ` +
      `${NO_UNVERIFIABLE_EXISTENCE_CLAIMS} Reply verdict: ACCEPT or REJECT, with reasoning. ${NO_DISK_WRITES}`,
      { label: `${item.name}:judge`, phase: 'Validate', model: JUDGE_MODEL, effort: 'high', schema: VERDICT_SCHEMA, ...READ_ONLY }
    )
    log(`${item.name}: ${judged.verdict}.`)
    return { ...prev, judged, verdict: judged.verdict }
  }
)

const accepted = results.filter(Boolean).filter(r => r.verdict === 'ACCEPT')
const rejected = results.filter(Boolean).filter(r => r.verdict === 'REJECT')
const skippedNoIssues = results.filter(Boolean).filter(r => r.skip && r.reason === 'no-issues')
const skippedBudget = results.filter(Boolean).filter(r => r.skip && r.reason === 'budget')

log(`Done. ${accepted.length} accepted, ${rejected.length} rejected, ${skippedNoIssues.length} skipped (no issues), ${skippedBudget.length} skipped (budget).`)

return { accepted, rejected, skippedNoIssues: skippedNoIssues.map(r => r.name), skippedBudget: skippedBudget.map(r => r.name) }
