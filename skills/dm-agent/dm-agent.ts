/**
 * dm-agent: four pi tools over dm's own CLI surface.
 *
 * Each tool carries one action enum, and every action runs `dm` through
 * pi.exec and reads the JSON the CLI prints. No action parses prose: a failure
 * is a nonzero exit with stderr, and a reply is a JSON document.
 *
 * Verb and flag names come from internal/clidoc/clidoc.go. Where a verb's row
 * carries --json the action passes it. Where the CLI formats its reply for a
 * human, or where the request body arrives on stdin (the ask opens), the action
 * sends the same request through `dm exec` with the action JSON, whose stdout
 * is the server's JSON reply. README's RPC section names those actions.
 *
 * No runtime import of pi's packages: the test drives the file with plain node,
 * and only pi's loader needs the extension API type.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

export const TOOL_NAMES = ["dm_layout", "dm_pane", "dm_box", "dm_turn"] as const;

type ExecOptions = { signal?: AbortSignal; timeout?: number; maxBuffer?: number };
type ExecResult = {
	stdout: string;
	stderr: string;
	code: number;
	killed: boolean;
	truncated: boolean;
};
type Exec = (command: string, args: string[], options?: ExecOptions) => Promise<ExecResult>;

type ToolResult = {
	content: { type: "text"; text: string }[];
	details: Record<string, unknown>;
	structuredContent?: Record<string, unknown>;
	isError?: boolean;
};

/** One dm invocation: the argv that ran, and what it answered. */
type Call = {
	argv: string[];
	stdout: string;
	stderr: string;
	code: number;
	killed: boolean;
	truncated: boolean;
};

type Params = Record<string, any>;

/** The reply cap: a snapshot with every pane still fits well inside it. */
const MAX_BUFFER = 4 * 1024 * 1024;
const AWAIT_CAP_S = 25;
const DM_WAIT_DEFAULT_S = 300;
const SUB_DEFAULT_S = 15;

// The parameter schemas. A plain literal is JSON Schema, and it keeps a schema
// builder out of this file. One object per tool, with the action enum inside.

const layoutParameters = {
	type: "object",
	properties: {
		action: {
			type: "string",
			enum: ["list", "split", "window", "focus", "kill"],
			description: "list the panes, split one, open a window, focus one, or close one.",
		},
		session: { type: "string", description: "dm session name. Omit to use DM_SESSION or main." },
		pane: { type: "string", description: "Pane id the action names, such as w1p1. split, focus and kill need one." },
		dir: {
			type: "string",
			enum: ["left", "right", "up", "down"],
			description: "Side the split lands on. Default right.",
		},
		argv: {
			type: "array",
			items: { type: "string" },
			description: "Program the new window runs, program first. Empty means the login shell.",
		},
		full: {
			type: "boolean",
			description:
				"list only: print dm's raw snapshot JSON instead of one line per pane. A script reads the snapshot from structuredContent without it.",
		},
	},
	required: ["action"],
	additionalProperties: false,
};

const paneParameters = {
	type: "object",
	properties: {
		action: {
			type: "string",
			enum: ["write", "read", "wait", "status"],
			description: "type into a pane, print its screen, wait for a command, or read its status.",
		},
		session: { type: "string", description: "dm session name. Omit to use DM_SESSION or main." },
		pane: { type: "string", description: "Pane id, such as w1p1. Every action but status needs one." },
		msg: { type: "string", description: "Text write types into the pane." },
		enter: { type: "boolean", description: "Press enter after the text. Default false." },
		lines: { type: "integer", minimum: 1, description: "Keep only the last N screen lines on read." },
		scrollback: {
			type: "integer",
			minimum: 1,
			description: "Read N history rows above the screen instead of the visible screen.",
		},
		timeout: {
			type: "integer",
			minimum: 1,
			description: `Quiet-idle limit in seconds for wait. Default ${DM_WAIT_DEFAULT_S}.`,
		},
	},
	required: ["action"],
	additionalProperties: false,
};

const boxParameters = {
	type: "object",
	properties: {
		action: {
			type: "string",
			enum: ["post", "list", "wait", "sub"],
			description: "post one entry, list the box, block for one entry, or stream entries.",
		},
		session: { type: "string", description: "dm session name. Omit to use DM_SESSION or main." },
		key: { type: "string", description: "Exact box topic, such as lane/provision/w2." },
		prefix: { type: "string", description: "Box topic prefix, such as lane/provision/. Use instead of key." },
		state: {
			type: "string",
			enum: ["note", "working", "blocked", "done", "error"],
			description: "Entry state. post needs one; wait may filter on one.",
		},
		msg: { type: "string", description: "The one-line message to post." },
		body: { type: "string", description: "Long body under the message line. Newlines allowed." },
		from: { type: "string", description: "Pane that posts. Default DM_PANE when pi runs in a dm pane." },
		after: { type: "integer", minimum: 0, description: "Cursor: only entries past this seq." },
		limit: { type: "integer", minimum: 0, description: "Rows a list may return. 0 means the whole box." },
		timeout: {
			type: "integer",
			minimum: 1,
			description: `Wait timeout in seconds. Default ${DM_WAIT_DEFAULT_S}; sub defaults to ${SUB_DEFAULT_S}.`,
		},
	},
	required: ["action"],
	additionalProperties: false,
};

const questionSchema = {
	type: "array",
	minItems: 1,
	description: "The questions to put to the operator.",
	items: {
		type: "object",
		properties: {
			question: { type: "string", description: "The complete question, ending with a question mark." },
			header: { type: "string", description: "Short chip label for the question." },
			multiSelect: { type: "boolean", description: "True when several options may be picked." },
			options: {
				type: "array",
				description: "The choices on offer. Leave empty for a free text question.",
				items: {
					type: "object",
					properties: {
						label: { type: "string", description: "The choice text." },
						description: { type: "string", description: "What the choice means, or its trade-off." },
						preview: { type: "string", description: "Optional markdown shown beside the options." },
					},
					required: ["label"],
					additionalProperties: false,
				},
			},
		},
		required: ["question", "options"],
		additionalProperties: false,
	},
};

const answerSchema = {
	type: "array",
	description: "One item per answered question.",
	items: {
		type: "object",
		properties: {
			questionIndex: { type: "integer", minimum: 0, description: "Index into the ask's questions." },
			optionIndexes: {
				type: "array",
				items: { type: "integer", minimum: 0 },
				description: "Option indexes the reader picked, in authored order.",
			},
			text: { type: "string", description: "Custom text the reader typed." },
			notes: { type: "string", description: "The reader's note on this question." },
		},
		required: ["questionIndex"],
		additionalProperties: false,
	},
};

const turnParameters = {
	type: "object",
	properties: {
		action: {
			type: "string",
			enum: ["ask", "answer", "await"],
			description: "open a question set, answer one as the reader, or block for an answer.",
		},
		session: { type: "string", description: "dm session name. Omit to use DM_SESSION or main." },
		pane: { type: "string", description: "Pane the ask opens on. ask needs one." },
		askId: { type: "string", description: "Ask id, such as a1. answer and await need one." },
		questions: questionSchema,
		items: answerSchema,
		globalNote: { type: "string", description: "The reader's note on the whole ask." },
		cancel: { type: "boolean", description: "Answer with a decline." },
		timeout: {
			type: "integer",
			minimum: 1,
			description: "How long await keeps waiting, in seconds. Default 120.",
		},
	},
	required: ["action"],
	additionalProperties: false,
};

const LAYOUT_GUIDELINES = [
	"Use dm_layout with action list before you split or focus: its JSON names every pane id, window and focus mark.",
	"Use dm_layout with action split to place a new pane beside a named pane, because dm_layout never guesses a target.",
	"Use dm_layout with action window when the work deserves its own window instead of half of the current one.",
	"Use dm_layout with action focus to move the operator's attention, and dm_layout with action kill only on a pane you opened.",
];

const PANE_GUIDELINES = [
	"Use dm_pane with action write to type a command into a pane; pass enter true to submit it.",
	"Use dm_pane with action wait after a write, not a dm_pane read loop: wait returns the exit code when the command ends.",
	"Use dm_pane with action read to inspect a screen you are not attached to, and ask for the fewest lines that settle the question.",
	"Use dm_pane with action status to read the agent state a pane reports; do not use it to poll for output.",
];

const BOX_GUIDELINES = [
	"Use dm_box with action post to record one lane state on a key such as lane/<encounter>/<minion>.",
	"Use dm_box with action wait to block for a lane's next entry; do not poll with dm_box list in a loop.",
	"Use dm_box with action sub only when a scripted step must follow a whole prefix of keys.",
	"Use dm_box with action list to catch up from a cursor, and pass the seq you last saw as after.",
];

const TURN_GUIDELINES = [
	"Use dm_turn with action ask to put a question set to the operator on a pane; it returns the ask id.",
	"Use dm_turn with action await after dm_turn ask; it returns the answer, or pending when the deadline passes.",
	"Use dm_turn with action answer only when you are the reader of an ask another agent opened.",
];

// Plumbing.

function ok(text: string, details: Record<string, unknown>): ToolResult {
	return { content: [{ type: "text", text }], details };
}

function toolError(text: string, details: Record<string, unknown> = {}): ToolResult {
	return { content: [{ type: "text", text }], details: { ...details, error: true }, isError: true };
}

function num(value: unknown, def: number): number {
	return typeof value === "number" && Number.isFinite(value) && value > 0 ? Math.floor(value) : def;
}

function str(value: unknown): string | undefined {
	return typeof value === "string" && value.trim() !== "" ? value.trim() : undefined;
}

function need(value: unknown, name: string): string {
	const s = str(value);
	if (!s) throw new Error(`${name} is required`);
	return s;
}

/** Append -s when the caller named a session. dm reorders flags before positionals. */
function withSession(argv: string[], session: string | undefined): string[] {
	return session ? [...argv, "-s", session] : argv;
}

/** The one JSON positional `dm exec` accepts. */
function execArgv(action: string): string[] {
	return ["exec", JSON.stringify(action)];
}

async function dm(exec: Exec, argv: string[], opts: { signal?: AbortSignal; timeoutMs?: number }): Promise<Call> {
	try {
		const r = await exec("dm", argv, {
			signal: opts.signal,
			timeout: opts.timeoutMs,
			maxBuffer: MAX_BUFFER,
		});
		return { argv, ...r };
	} catch (err) {
		return {
			argv,
			stdout: "",
			stderr: err instanceof Error ? err.message : String(err),
			code: 127,
			killed: false,
			truncated: false,
		};
	}
}

/** A nonzero exit reports the CLI's own text. Nothing here reads a success. */
function failed(c: Call): string | undefined {
	if (c.killed) return `dm ${c.argv.join(" ")} was killed: timeout, abort or output over the cap`;
	if (c.code === 0) return undefined;
	const text = (c.stderr || c.stdout).trim();
	return text || `dm ${c.argv.join(" ")} exited ${c.code}`;
}

function parseJSON<T>(c: Call, what: string): T {
	const raw = c.stdout.trim();
	if (!raw) throw new Error(`${what}: dm printed no JSON`);
	try {
		return JSON.parse(raw) as T;
	} catch {
		throw new Error(`${what}: dm printed no JSON: ${raw.split("\n")[0].slice(0, 200)}`);
	}
}

/** Parse NDJSON: one JSON document per non-blank line. */
function parseLines<T>(c: Call, what: string): T[] {
	const out: T[] = [];
	for (const line of c.stdout.split("\n")) {
		const raw = line.trim();
		if (!raw) continue;
		try {
			out.push(JSON.parse(raw) as T);
		} catch {
			throw new Error(`${what}: dm printed a line that is not JSON: ${raw.slice(0, 200)}`);
		}
	}
	return out;
}

function entryLine(e: any): string {
	return `${e.seq} ${e.key} ${e.state} ${e.from || "-"} ${e.msg}`;
}

function answerText(a: any): string {
	if (a?.cancel) return "the reader declined the ask";
	const items = Array.isArray(a?.items) ? a.items : [];
	const parts = items.map((i: any) => {
		const picks = Array.isArray(i.optionIndexes) && i.optionIndexes.length > 0
			? `options ${i.optionIndexes.join(",")}`
			: "";
		return [`q${i.questionIndex}`, picks, i.text ? `text ${JSON.stringify(i.text)}` : "", i.notes ? `notes ${JSON.stringify(i.notes)}` : ""]
			.filter(Boolean)
			.join(" ");
	});
	const note = a?.globalNote ? ` global note ${JSON.stringify(a.globalNote)}` : "";
	return parts.length > 0 ? `answered: ${parts.join("; ")}${note}` : `answered${note}`;
}

// dm_layout: the pane tree.

async function runLayout(exec: Exec, p: Params, signal?: AbortSignal): Promise<ToolResult> {
	const session = str(p.session);
	const action = need(p.action, "action");

	if (action === "list") {
		const c = await dm(exec, withSession(["list", "--json"], session), { signal });
		const bad = failed(c);
		if (bad) return toolError(bad, { action, argv: c.argv });
		const reply = parseJSON<any>(c, "dm list");
		const snapshot = reply.snapshot ?? reply;
		if (p.full === true) return ok(c.stdout.trim(), { action, argv: c.argv, snapshot });
		return ok(paneLines(snapshot), { action, argv: c.argv, snapshot });
	}

	if (action === "split") {
		const pane = need(p.pane, "pane");
		const dir = str(p.dir) ?? "right";
		const c = await dm(exec, withSession(["split", pane, "--dir", dir, "--json"], session), { signal });
		const bad = failed(c);
		if (bad) return toolError(bad, { action, argv: c.argv });
		const reply = parseJSON<{ pane?: string }>(c, "dm split");
		return ok(`split ${pane} ${dir} -> ${reply.pane ?? "(no id)"}`, { action, argv: c.argv, pane: reply.pane, from: pane, dir });
	}

	if (action === "window") {
		const argv: string[] = Array.isArray(p.argv) ? p.argv.filter((w: unknown) => typeof w === "string") : [];
		const extra = argv.flatMap((w) => ["--argv", w]);
		const c = await dm(exec, withSession(["new-window", ...extra], session), { signal });
		const bad = failed(c);
		if (bad) return toolError(bad, { action, argv: c.argv });
		const reply = c.stdout.trim() ? parseJSON<{ window?: string; pane?: string }>(c, "dm new-window") : {};
		return ok(`new window ${reply.window ?? "?"}, pane ${reply.pane ?? "?"}`, { action, argv: c.argv, ...reply });
	}

	if (action === "focus") {
		const pane = need(p.pane, "pane");
		const c = await dm(exec, withSession(["focus", pane], session), { signal });
		const bad = failed(c);
		if (bad) return toolError(bad, { action, argv: c.argv });
		return ok(`focused ${pane}`, { action, argv: c.argv });
	}

	if (action === "kill") {
		const pane = need(p.pane, "pane");
		// clidoc has no close verb; README's RPC list names close-pane, and
		// dm exec sends it as JSON.
		const c = await dm(exec, withSession(execArgv({ action: "close-pane", target: pane }), session), { signal });
		const bad = failed(c);
		if (bad) return toolError(bad, { action: "kill", argv: c.argv });
		return ok(`closed ${pane}`, { action: "kill", argv: c.argv, pane });
	}

	return toolError(`unknown action ${action}`, { action });
}

/**
 * paneLines renders one line per pane: the id, then the project, branch and
 * status, then the marks that change a decision (shelf, a dirty tree, the app
 * the pane runs). `dm list --json` is 7 kB of daemon detail for a twelve pane
 * session; this is the slice a model needs to choose a pane. A script still
 * gets the whole object in structuredContent.
 */
function paneLines(snapshot: any): string {
	const panes: any[] = Array.isArray(snapshot?.all) ? snapshot.all : [];
	if (panes.length === 0) return "(no panes)";
	return panes
		.map((q) => {
			const mark = q.focus ? "*" : " ";
			const head = [q.project ?? "-", q.branch ?? "-", q.status ?? "-"].join(" ");
			const marks = [
				q.shelf ? "shelf" : "",
				q.dirty ? `dirty ${q.added ?? 0}+ ${q.deleted ?? 0}-` : "",
				q.app ?? "",
			].filter(Boolean);
			return `${mark}${q.id} ${head}${marks.length > 0 ? " " + marks.join(" ") : ""}`;
		})
		.join("\n");
}

// dm_pane: one pane's screen, input, wait and status.

async function runPane(exec: Exec, p: Params, signal?: AbortSignal): Promise<ToolResult> {
	const session = str(p.session);
	const action = need(p.action, "action");

	if (action === "write") {
		const pane = need(p.pane, "pane");
		const msg = need(p.msg, "msg");
		const argv = ["write-to", pane, "--msg", msg, ...(p.enter ? ["--enter"] : [])];
		const c = await dm(exec, withSession(argv, session), { signal });
		const bad = failed(c);
		if (bad) return toolError(bad, { action, argv: c.argv });
		return ok(`wrote to ${pane}${p.enter ? " and pressed enter" : ""}`, { action, argv: c.argv, pane });
	}

	if (action === "read") {
		const pane = need(p.pane, "pane");
		const scrollback = num(p.scrollback, 0);
		const req = scrollback > 0
			? { action: "history", target: pane, count: scrollback }
			: { action: "read", target: pane };
		const c = await dm(exec, withSession(execArgv(req), session), { signal });
		const bad = failed(c);
		if (bad) return toolError(bad, { action, argv: c.argv });
		const reply = parseJSON<{ lines?: string[]; status?: unknown }>(c, "dm read");
		let lines = reply.lines ?? [];
		const keep = num(p.lines, 0);
		if (keep > 0 && lines.length > keep) lines = lines.slice(lines.length - keep);
		return ok(lines.join("\n") || "(blank screen)", {
			action,
			argv: c.argv,
			lines,
			status: reply.status,
			scrollback: scrollback > 0 ? scrollback : undefined,
		});
	}

	if (action === "wait") {
		const pane = need(p.pane, "pane");
		const secs = num(p.timeout, DM_WAIT_DEFAULT_S);
		const c = await dm(exec, withSession(["wait", pane, "--timeout", `${secs}s`, "--json"], session), {
			signal,
			timeoutMs: (secs + 10) * 1000,
		});
		if (c.killed) return toolError(failed(c) ?? "wait was killed", { action, argv: c.argv });
		// A nonzero exit is the observed command's code, not a dm failure:
		// clidoc documents that dm wait exits with the code it reports. The
		// JSON a real failure carries no reply, so parseJSON is the failure.
		const reply = parseJSON<{ exit?: number; reason?: string; timed_out?: boolean }>(c, "dm wait");
		const text = reply.timed_out
			? `timeout after ${secs}s: no exit code observed`
			: `exit ${reply.exit ?? "?"} (${reply.reason ?? "unknown"})`;
		return ok(`pane ${pane}: ${text}`, { action, argv: c.argv, ...reply });
	}

	if (action === "status") {
		const pane = str(p.pane);
		const req = pane ? { action: "status", target: pane } : { action: "status" };
		const c = await dm(exec, withSession(execArgv(req), session), { signal });
		const bad = failed(c);
		if (bad) return toolError(bad, { action, argv: c.argv });
		const reply = parseJSON<any>(c, "dm status");
		return ok(JSON.stringify(reply), { action, argv: c.argv, status: reply });
	}

	return toolError(`unknown action ${action}`, { action });
}

// dm_box: the message box.

function topicCheck(p: Params): void {
	if (str(p.key) && str(p.prefix)) throw new Error("key and prefix are exclusive");
}

async function runBox(exec: Exec, p: Params, signal?: AbortSignal): Promise<ToolResult> {
	const session = str(p.session);
	const action = need(p.action, "action");
	topicCheck(p);

	if (action === "post") {
		const key = need(p.key, "key");
		const state = need(p.state, "state");
		const msg = need(p.msg, "msg");
		const from = str(p.from) ?? process.env.DM_PANE ?? "";
		const body = typeof p.body === "string" ? p.body : "";
		// post prints a row, not JSON; README's msg-post reply is {"entry":...}.
		const req = { action: "msg-post", res: { key, state, msg, body, from } };
		const c = await dm(exec, withSession(execArgv(req), session), { signal });
		const bad = failed(c);
		if (bad) return toolError(bad, { action, argv: c.argv });
		const reply = parseJSON<{ entry?: any }>(c, "dm msg post");
		return ok(entryLine(reply.entry), { action, argv: c.argv, entry: reply.entry });
	}

	if (action === "list") {
		const argv = ["msg", "list", "--json"];
		if (str(p.key)) argv.push("--key", str(p.key) as string);
		if (str(p.prefix)) argv.push("--prefix", str(p.prefix) as string);
		if (typeof p.after === "number" && p.after >= 0) argv.push("--after", String(p.after));
		if (typeof p.limit === "number" && p.limit >= 0) argv.push("--limit", String(p.limit));
		const c = await dm(exec, withSession(argv, session), { signal });
		const bad = failed(c);
		if (bad) return toolError(bad, { action, argv: c.argv });
		const reply = parseJSON<{ entries?: any[]; trimmed?: boolean }>(c, "dm msg list");
		const entries = reply.entries ?? [];
		const text = entries.length > 0 ? entries.map(entryLine).join("\n") : "(no entries)";
		return ok(text, { action, argv: c.argv, entries, trimmed: reply.trimmed });
	}

	if (action === "wait") {
		const argv = ["msg", "wait", "--json"];
		if (str(p.key)) argv.push("--key", str(p.key) as string);
		if (str(p.prefix)) argv.push("--prefix", str(p.prefix) as string);
		if (str(p.state)) argv.push("--state", str(p.state) as string);
		if (typeof p.after === "number" && p.after >= 0) argv.push("--after", String(p.after));
		const secs = num(p.timeout, DM_WAIT_DEFAULT_S);
		argv.push("--timeout", `${secs}s`);
		const c = await dm(exec, withSession(argv, session), { signal, timeoutMs: (secs + 10) * 1000 });
		const bad = failed(c);
		if (bad) return toolError(bad, { action, argv: c.argv });
		const reply = parseJSON<{ entry?: any; trimmed?: boolean }>(c, "dm msg wait");
		return ok(reply.entry ? entryLine(reply.entry) : "(no entry)", {
			action,
			argv: c.argv,
			entry: reply.entry,
			trimmed: reply.trimmed,
		});
	}

	if (action === "sub") {
		const argv = ["msg", "sub", "--json"];
		if (str(p.key)) argv.push("--key", str(p.key) as string);
		if (str(p.prefix)) argv.push("--prefix", str(p.prefix) as string);
		if (str(p.state)) argv.push("--state", str(p.state) as string);
		if (typeof p.after === "number" && p.after >= 0) argv.push("--after", String(p.after));
		const secs = num(p.timeout, SUB_DEFAULT_S);
		// sub streams until its connection drops, so pi.exec stops it at the
		// deadline and the frames already printed are the answer.
		const c = await dm(exec, withSession(argv, session), { signal, timeoutMs: secs * 1000 });
		const frames = parseLines<{ entry?: any; trimmed?: boolean }>(c, "dm msg sub");
		const entries = frames.map((f) => f.entry).filter(Boolean);
		const bad = c.killed ? undefined : failed(c);
		if (bad) return toolError(bad, { action, argv: c.argv });
		const text = entries.length > 0 ? entries.map(entryLine).join("\n") : `(no entries in ${secs}s)`;
		return ok(text, { action, argv: c.argv, entries, killed: c.killed, seconds: secs });
	}

	return toolError(`unknown action ${action}`, { action });
}

// dm_turn: the ask store.

async function runTurn(exec: Exec, p: Params, toolCallId: string, signal?: AbortSignal): Promise<ToolResult> {
	const session = str(p.session);
	const action = need(p.action, "action");

	if (action === "ask") {
		const pane = need(p.pane, "pane");
		const questions = Array.isArray(p.questions) ? p.questions : [];
		if (questions.length === 0) throw new Error("questions must carry at least one question");
		// The store's body is OpenReq: the ask-open verb reads it on stdin, so
		// the same request rides dm exec.
		const req = { action: "ask-open", target: pane, res: { toolCallId, questions } };
		const c = await dm(exec, withSession(execArgv(req), session), { signal });
		const bad = failed(c);
		if (bad) return toolError(bad, { action, argv: c.argv });
		const reply = parseJSON<{ id?: string }>(c, "dm ask open");
		return ok(`ask ${reply.id ?? "?"} opened on ${pane}`, { action, argv: c.argv, askId: reply.id, pane });
	}

	if (action === "answer") {
		const askId = need(p.askId, "askId");
		const items = Array.isArray(p.items) ? p.items : [];
		const cancel = p.cancel === true;
		const globalNote = str(p.globalNote) ?? "";
		if (items.length === 0 && !cancel && !globalNote) throw new Error("answer needs items, cancel or globalNote");
		const req = { action: "ask-answer", target: askId, res: { items, globalNote, cancel } };
		const c = await dm(exec, withSession(execArgv(req), session), { signal });
		const bad = failed(c);
		if (bad) return toolError(bad, { action, argv: c.argv });
		const reply = c.stdout.trim() ? parseJSON<any>(c, "dm ask answer") : {};
		return ok(cancel ? `ask ${askId} declined` : `ask ${askId} answered`, { action, argv: c.argv, askId, answer: reply });
	}

	if (action === "await") {
		const askId = need(p.askId, "askId");
		const total = num(p.timeout, 120);
		const deadline = Date.now() + total * 1000;
		for (;;) {
			// clidoc caps one await at 25s, so the tool chains calls until the
			// caller's own deadline. The last call asks for what remains.
			const leftS = Math.max(1, Math.ceil((deadline - Date.now()) / 1000));
			const cap = Math.min(AWAIT_CAP_S, leftS);
			const c = await dm(exec, withSession(["ask", "await", askId, "--timeout", `${cap}s`, "--json"], session), {
				signal,
				timeoutMs: (cap + 10) * 1000,
			});
			const bad = failed(c);
			if (bad) return toolError(bad, { action, argv: c.argv });
			const reply = parseJSON<{ pending?: boolean; answer?: any }>(c, "dm ask await");
			if (reply.pending) {
				// Pending from a call that already covered the remaining budget
				// is the answer: nobody replied in the time the caller allowed.
				if (cap >= leftS) {
					return ok(`pending: nobody answered ask ${askId} within ${total}s`, { action, argv: c.argv, askId, pending: true });
				}
				continue;
			}
			if (!reply.answer) return toolError("dm ask await carried neither pending nor an answer", { action, argv: c.argv, askId });
			return ok(answerText(reply.answer), { action, argv: c.argv, askId, answer: reply.answer });
		}
	}

	return toolError(`unknown action ${action}`, { action });
}

/** Every handler answers with a ToolResult: a throw is a refusal, not a crash. */
async function guard(action: string, fn: () => Promise<ToolResult>): Promise<ToolResult> {
	try {
		return await fn();
	} catch (err) {
		return toolError(err instanceof Error ? err.message : String(err), { action });
	}
}

// replySchema describes what a call hands back. A codemode script reads
// structuredContent, which is the same object as details, so the parsed reply
// reaches a script as fields instead of prose it would have to parse.
const replySchema = {
	type: "object",
	additionalProperties: true,
	description: "The parsed dm reply, the same object as details.",
};

// exposureFor names how the model reaches one tool. dm_turn asks the operator,
// so pi's own rule makes it model-only: declared and never callable from a
// script. The other three are plumbing, so they stay direct: declared to the
// model and callable from a codemode script, which is where a filtered tree
// read or a join over dm's state belongs, instead of in the context window.
function exposureFor(name: string): "direct" | "model-only" {
	return name === "dm_turn" ? "model-only" : "direct";
}

export default function (pi: ExtensionAPI) {
	const exec = pi.exec.bind(pi) as Exec;

	const tools = [
		{
			name: "dm_layout",
			label: "dm layout",
			description:
				"List, split, open, focus or close the panes and windows of a dm session. List prints one line per pane; full prints the raw snapshot.",
			promptSnippet: "List, split, open, focus or close dm panes and windows when the work needs another terminal.",
			promptGuidelines: LAYOUT_GUIDELINES,
			parameters: layoutParameters,
			run: (p: Params, _id: string, signal?: AbortSignal) => runLayout(exec, p, signal),
		},
		{
			name: "dm_pane",
			label: "dm pane",
			description:
				"Write to a dm pane, read its screen, wait for its command to end, or read the agent status it reports.",
			promptSnippet: "Write to, read from, wait on or report the status of one dm pane.",
			promptGuidelines: PANE_GUIDELINES,
			parameters: paneParameters,
			run: (p: Params, _id: string, signal?: AbortSignal) => runPane(exec, p, signal),
		},
		{
			name: "dm_box",
			label: "dm box",
			description:
				"Post one entry to dm's message box, list a topic or prefix, block for the next matching entry, or stream entries.",
			promptSnippet: "Post, list, wait on or subscribe to dm's message box for lane and worker handoffs.",
			promptGuidelines: BOX_GUIDELINES,
			parameters: boxParameters,
			run: (p: Params, _id: string, signal?: AbortSignal) => runBox(exec, p, signal),
		},
		{
			name: "dm_turn",
			label: "dm turn",
			description:
				"Ask the operator a question set through dm's ask store, answer one as the reader, or await an answer.",
			promptSnippet: "Ask the operator through dm's ask store, or answer and await an ask as the reader.",
			promptGuidelines: TURN_GUIDELINES,
			parameters: turnParameters,
			run: (p: Params, id: string, signal?: AbortSignal) => runTurn(exec, p, id, signal),
		},
	];

	for (const tool of tools) {
		pi.registerTool({
			name: tool.name,
			label: tool.label,
			description: tool.description,
			promptSnippet: tool.promptSnippet,
			promptGuidelines: tool.promptGuidelines,
			exposure: exposureFor(tool.name),
			outputSchema: replySchema,
			parameters: tool.parameters,
			async execute(
				toolCallId: string,
				params: Params,
				signal: AbortSignal | undefined,
			): Promise<ToolResult> {
				const action = typeof params?.action === "string" ? params.action : "";
				if (!action) return toolError("action is required", { action });
				const res = await guard(action, () => tool.run(params, toolCallId, signal));
				return { ...res, structuredContent: res.details };
			},
		} as never);
	}
}
