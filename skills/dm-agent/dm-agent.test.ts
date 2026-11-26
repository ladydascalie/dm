/**
 * The extension against a fake pi and a fake dm on PATH. No session, no real
 * store: the fake dm records every argv and stdin, and answers from files
 * beside itself. The fake pi.exec spawns it the way pi does, timeout included.
 */

import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { test } from "node:test";

import registerDM, { TOOL_NAMES } from "./dm-agent.ts";

type Call = { argv: string[]; body: string };
type ExecResult = { stdout: string; stderr: string; code: number; killed: boolean; truncated: boolean };
type Tool = {
	exposure?: string;
	name: string;
	parameters: any;
	promptSnippet?: string;
	promptGuidelines?: string[];
	outputSchema?: any;
	execute(
		toolCallId: string,
		params: unknown,
		signal: AbortSignal | undefined,
		onUpdate: unknown,
		ctx: unknown,
	): Promise<{ content: { type: string; text: string }[]; details: Record<string, any>; structuredContent?: Record<string, any>; isError?: boolean }>;
};

const FAKE_DM = `#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");
const dir = process.env.FAKE_DM_DIR;
const argv = process.argv.slice(2);
let body = "";
try { body = fs.readFileSync(0, "utf8"); } catch (e) {}
fs.appendFileSync(path.join(dir, "calls.jsonl"), JSON.stringify({ argv, body }) + "\\n");

let action = "";
for (const a of argv) {
  if (a.trim().startsWith("{")) {
    try { action = JSON.parse(a).action || ""; } catch (e) { action = ""; }
    break;
  }
}
let slot;
if (action) slot = "exec." + action;
else if (argv[0] === "msg" || argv[0] === "ask") slot = argv[0] + "." + (argv[1] || "");
else slot = argv[0] || "";

const file = (n) => path.join(dir, n);
const read = (n) => (fs.existsSync(file(n)) ? fs.readFileSync(file(n), "utf8") : "");
const count = file("count." + slot);
const n = fs.existsSync(count) ? Number(fs.readFileSync(count, "utf8").trim() || "0") : 0;
fs.writeFileSync(count, String(n + 1));

const bad = file("err." + slot);
if (fs.existsSync(bad)) {
  process.stderr.write(fs.readFileSync(bad, "utf8"));
  process.exit(Number(read("exit." + slot)) || 1);
}
const code = file("exit." + slot);
if (fs.existsSync(code)) {
  fs.writeSync(1, read("resp." + slot + "." + n) || read("resp." + slot));
  process.exit(Number(fs.readFileSync(code, "utf8").trim()) || 1);
}
const seq = file("resp." + slot + "." + n);
const resp = fs.existsSync(seq) ? fs.readFileSync(seq, "utf8") : read("resp." + slot);
fs.writeSync(1, resp);
if (slot === "msg.sub") {
  setInterval(() => {}, 1000);
} else {
  process.exit(0);
}
`;

type Sandbox = {
	base: string;
	state: string;
	tools: Map<string, Tool>;
	calls: () => Call[];
	resp(slot: string, value: unknown, index?: number): void;
	fail(slot: string, text: string): void;
	exit(slot: string, code: number): void;
	setPane(pane: string | undefined): void;
	tool(name: string): Tool;
};

const saved = {
	PATH: process.env.PATH,
	DM_PANE: process.env.DM_PANE,
	DM_SESSION: process.env.DM_SESSION,
	FAKE_DM_DIR: process.env.FAKE_DM_DIR,
};

function restore(): void {
	for (const [key, value] of Object.entries(saved)) {
		if (value === undefined) delete process.env[key];
		else process.env[key] = value;
	}
}

/** pi.exec's contract: no shell, a shared output cap, a kill on timeout. */
function fakeExec(): (command: string, args: string[], options?: any) => Promise<ExecResult> {
	return (command, args, options = {}) =>
		new Promise((resolve) => {
			// pi.exec gives the child no stdin, so a fake dm that reads fd 0
			// sees EOF instead of blocking on a pipe nobody closes.
			const child = spawn(command, args, { env: process.env, stdio: ["ignore", "pipe", "pipe"] });
			let stdout = "";
			let stderr = "";
			let killed = false;
			let done = false;
			const finish = (code: number) => {
				if (done) return;
				done = true;
				if (timer) clearTimeout(timer);
				resolve({ stdout, stderr, code, killed, truncated: false });
			};
			const kill = () => {
				killed = true;
				child.kill("SIGKILL");
			};
			const timer = options.timeout ? setTimeout(kill, options.timeout) : undefined;
			options.signal?.addEventListener("abort", kill);
			child.stdout.on("data", (d: Buffer) => {
				stdout += d.toString();
			});
			child.stderr.on("data", (d: Buffer) => {
				stderr += d.toString();
			});
			child.on("error", (err) => {
				stderr += String(err);
				finish(127);
			});
			child.on("close", (code, signal) => finish(code ?? (signal ? 137 : 1)));
		});
}

function sandbox(t: { after(fn: () => void): void }): Sandbox {
	const base = fs.mkdtempSync(path.join(os.tmpdir(), "dm-agent-"));
	const bin = path.join(base, "bin");
	const state = path.join(base, "state");
	fs.mkdirSync(bin);
	fs.mkdirSync(state);
	const dmPath = path.join(bin, "dm");
	fs.writeFileSync(dmPath, FAKE_DM);
	fs.chmodSync(dmPath, 0o755);

	process.env.FAKE_DM_DIR = state;
	process.env.PATH = `${bin}:${saved.PATH ?? ""}`;
	delete process.env.DM_PANE;
	delete process.env.DM_SESSION;

	t.after(() => {
		restore();
		fs.rmSync(base, { recursive: true, force: true });
	});

	const tools = new Map<string, Tool>();
	const pi = {
		exec: fakeExec(),
		registerTool(definition: Tool) {
			tools.set(definition.name, definition);
		},
	};
	registerDM(pi as never);

	const callsFile = path.join(state, "calls.jsonl");
	return {
		base,
		state,
		tools,
		calls() {
			if (!fs.existsSync(callsFile)) return [];
			return fs
				.readFileSync(callsFile, "utf8")
				.split("\n")
				.filter((line) => line.trim() !== "")
				.map((line) => JSON.parse(line) as Call);
		},
		resp(slot: string, value: unknown, index?: number) {
			const name = index === undefined ? `resp.${slot}` : `resp.${slot}.${index}`;
			const text = typeof value === "string" ? value : JSON.stringify(value);
			fs.writeFileSync(path.join(state, name), text);
		},
		fail(slot: string, text: string) {
			fs.writeFileSync(path.join(state, `err.${slot}`), text);
			fs.writeFileSync(path.join(state, `exit.${slot}`), "1");
		},
		exit(slot: string, code: number) {
			fs.writeFileSync(path.join(state, `exit.${slot}`), String(code));
		},
		setPane(pane: string | undefined) {
			if (pane === undefined) delete process.env.DM_PANE;
			else process.env.DM_PANE = pane;
		},
		tool(name: string) {
			const found = tools.get(name);
			assert.ok(found, `the extension registered no ${name}`);
			return found;
		},
	};
}

function run(box: Sandbox, name: string, params: Record<string, unknown>) {
	return box.tool(name).execute("toolu_1", params, undefined, undefined, {});
}

test("registers four tools, each with an action enum and self-naming guidelines", (t) => {
	const box = sandbox(t);
	assert.deepEqual([...box.tools.keys()], [...TOOL_NAMES]);
	const expected: Record<string, string[]> = {
		dm_layout: ["list", "split", "window", "focus", "kill"],
		dm_pane: ["write", "read", "wait", "status"],
		dm_box: ["post", "list", "wait", "sub"],
		dm_turn: ["ask", "answer", "await"],
	};
	for (const [name, actions] of Object.entries(expected)) {
		const tool = box.tool(name);
		assert.deepEqual(tool.parameters.properties.action.enum, actions, name);
		assert.equal(typeof tool.promptSnippet, "string", name);
		assert.ok(tool.promptSnippet && tool.promptSnippet.length > 0, name);
		const guidelines = tool.promptGuidelines ?? [];
		assert.ok(guidelines.length > 0, name);
		for (const line of guidelines) assert.match(line, new RegExp(name), name);
	}
	// dm_turn asks the operator, so pi keeps it out of scripts. The other three
	// stay callable from codemode, where filtering a tree or looping on the box
	// costs no context.
	assert.equal(box.tool("dm_turn").exposure, "model-only");
	for (const name of TOOL_NAMES) {
		assert.equal(typeof box.tool(name).outputSchema, "object", name);
	}
	for (const name of ["dm_layout", "dm_pane", "dm_box"]) {
		assert.equal(box.tool(name).exposure, "direct", name);
	}
	assert.equal(box.tools.size, 4);
});


test("layout list prints one line per pane, and full prints the snapshot", async (t) => {
	const box = sandbox(t);
	const snapshot = {
		all: [
			{ id: "w13p1", focus: true, project: "dm", branch: "main", status: "working", app: "pi" },
			{ id: "w2p2", project: "publisher", branch: "main", dirty: true, added: 207, deleted: 6, shelf: true },
		],
	};
	box.resp("list", { snapshot });

	const result = await run(box, "dm_layout", { action: "list" });

	assert.deepEqual(box.calls().map((c) => c.argv), [["list", "--json"]]);
	assert.equal(result.isError, undefined);
	// One line per pane, with the focus star and the marks that change a decision.
	assert.equal(
		result.content[0].text,
		"*w13p1 dm main working pi\n w2p2 publisher main - shelf dirty 207+ 6-",
	);
	// The whole object still reaches a script, which is the point of the split.
	assert.deepEqual(result.details.snapshot, snapshot);
	assert.deepEqual(result.structuredContent, result.details);

	const full = await run(box, "dm_layout", { action: "list", full: true });
	assert.equal(full.content[0].text, JSON.stringify({ snapshot }));
});

test("layout split names the pane and the direction", async (t) => {
	const box = sandbox(t);
	box.resp("split", { pane: "w1p2" });

	const result = await run(box, "dm_layout", { action: "split", pane: "w1p1", dir: "down" });

	assert.deepEqual(box.calls()[0].argv, ["split", "w1p1", "--dir", "down", "--json"]);
	assert.equal(result.details.pane, "w1p2");
	assert.equal(result.content[0].text, "split w1p1 down -> w1p2");
});

test("layout window carries every argv word and reads the JSON reply", async (t) => {
	const box = sandbox(t);
	box.resp("new-window", { window: "w2", pane: "w2p1" });

	const result = await run(box, "dm_layout", { action: "window", argv: ["htop", "-d"] });

	assert.deepEqual(box.calls()[0].argv, ["new-window", "--argv", "htop", "--argv", "-d"]);
	assert.equal(result.details.window, "w2");
	assert.equal(result.content[0].text, "new window w2, pane w2p1");
});

test("layout focus runs the verb, and a session rides -s", async (t) => {
	const box = sandbox(t);
	box.resp("focus", "");

	const result = await run(box, "dm_layout", { action: "focus", pane: "w1p2", session: "base" });

	assert.deepEqual(box.calls()[0].argv, ["focus", "w1p2", "-s", "base"]);
	assert.equal(result.isError, undefined);
	assert.equal(result.content[0].text, "focused w1p2");
});

test("layout kill closes the pane through the close-pane action", async (t) => {
	const box = sandbox(t);
	box.resp("exec.close-pane", "");

	const result = await run(box, "dm_layout", { action: "kill", pane: "w1p1" });

	assert.deepEqual(box.calls()[0].argv, ["exec", JSON.stringify({ action: "close-pane", target: "w1p1" })]);
	assert.equal(result.isError, undefined);
	assert.equal(result.content[0].text, "closed w1p1");
});

test("pane write types the text and presses enter", async (t) => {
	const box = sandbox(t);
	box.resp("write-to", { pane: "w1p1" });

	const result = await run(box, "dm_pane", { action: "write", pane: "w1p1", msg: "ls -la", enter: true });

	assert.deepEqual(box.calls()[0].argv, ["write-to", "w1p1", "--msg", "ls -la", "--enter"]);
	assert.equal(result.content[0].text, "wrote to w1p1 and pressed enter");
});

test("pane read asks the RPC for the screen and keeps the last lines", async (t) => {
	const box = sandbox(t);
	box.resp("exec.read", { lines: ["one", "two", "three"], status: { state: "idle" } });

	const result = await run(box, "dm_pane", { action: "read", pane: "w1p1", lines: 2 });

	assert.deepEqual(box.calls()[0].argv, ["exec", JSON.stringify({ action: "read", target: "w1p1" })]);
	assert.equal(result.content[0].text, "two\nthree");
	assert.deepEqual(result.details.lines, ["two", "three"]);
	assert.deepEqual(result.details.status, { state: "idle" });
});

test("pane read asks for history rows when scrollback is named", async (t) => {
	const box = sandbox(t);
	box.resp("exec.history", { lines: ["old"] });

	const result = await run(box, "dm_pane", { action: "read", pane: "w1p1", scrollback: 50 });

	assert.deepEqual(
		box.calls()[0].argv,
		["exec", JSON.stringify({ action: "history", target: "w1p1", count: 50 })],
	);
	assert.equal(result.content[0].text, "old");
});

test("pane read refuses prose instead of parsing it", async (t) => {
	const box = sandbox(t);
	box.resp("exec.read", "hello from a terminal\n");

	const result = await run(box, "dm_pane", { action: "read", pane: "w1p1" });

	assert.equal(result.isError, true);
	assert.match(result.content[0].text, /no JSON/);
});

test("pane wait reports the exit code even when dm exits with it", async (t) => {
	const box = sandbox(t);
	box.resp("wait", { exit: 3, reason: "mark", timed_out: false });
	box.exit("wait", 3);

	const result = await run(box, "dm_pane", { action: "wait", pane: "w1p1", timeout: 30 });

	assert.deepEqual(box.calls()[0].argv, ["wait", "w1p1", "--timeout", "30s", "--json"]);
	assert.equal(result.isError, undefined);
	assert.equal(result.details.exit, 3);
	assert.equal(result.content[0].text, "pane w1p1: exit 3 (mark)");
});

test("pane status reads one pane through the status action", async (t) => {
	const box = sandbox(t);
	box.resp("exec.status", { state: "working", app: "pi" });

	const result = await run(box, "dm_pane", { action: "status", pane: "w1p1" });

	assert.deepEqual(box.calls()[0].argv, ["exec", JSON.stringify({ action: "status", target: "w1p1" })]);
	assert.deepEqual(result.details.status, { state: "working", app: "pi" });
});

test("box post sends the entry through msg-post and fills from DM_PANE", async (t) => {
	const box = sandbox(t);
	box.setPane("w1p1");
	box.resp("exec.msg-post", { entry: { seq: 4, key: "lane/a", state: "done", from: "w1p1", msg: "green" } });

	const result = await run(box, "dm_box", { action: "post", key: "lane/a", state: "done", msg: "green" });

	const req = { action: "msg-post", res: { key: "lane/a", state: "done", msg: "green", body: "", from: "w1p1" } };
	assert.deepEqual(box.calls()[0].argv, ["exec", JSON.stringify(req)]);
	assert.equal(result.content[0].text, "4 lane/a done w1p1 green");
});

test("box list passes key, cursor and limit", async (t) => {
	const box = sandbox(t);
	box.resp("msg.list", { entries: [{ seq: 8, key: "lane/a", state: "done", from: "w2p1", msg: "green" }], trimmed: false });

	const result = await run(box, "dm_box", { action: "list", key: "lane/a", after: 7, limit: 5 });

	assert.deepEqual(box.calls()[0].argv, ["msg", "list", "--json", "--key", "lane/a", "--after", "7", "--limit", "5"]);
	assert.deepEqual(result.details.entries, [
		{ seq: 8, key: "lane/a", state: "done", from: "w2p1", msg: "green" },
	]);
	assert.equal(result.content[0].text, "8 lane/a done w2p1 green");
});

test("box wait blocks with a prefix, a state and a timeout", async (t) => {
	const box = sandbox(t);
	box.resp("msg.wait", { entry: { seq: 9, key: "lane/b", state: "done", from: "w3p1", msg: "ok" }, trimmed: false });

	const result = await run(box, "dm_box", { action: "wait", prefix: "lane/", state: "done", after: 7, timeout: 600 });

	assert.deepEqual(
		box.calls()[0].argv,
		["msg", "wait", "--json", "--prefix", "lane/", "--state", "done", "--after", "7", "--timeout", "600s"],
	);
	assert.equal(result.details.entry.seq, 9);
});

test("box sub collects the JSON frames printed before the deadline", async (t) => {
	const box = sandbox(t);
	box.resp(
		"msg.sub",
		`${JSON.stringify({ entry: { seq: 1, key: "lane/a", state: "working", from: "w2p1", msg: "start" }, trimmed: false })}\n` +
			`${JSON.stringify({ entry: { seq: 2, key: "lane/a", state: "done", from: "w2p1", msg: "end" }, trimmed: false })}\n`,
	);

	const result = await run(box, "dm_box", { action: "sub", prefix: "lane/", timeout: 1 });

	assert.deepEqual(box.calls()[0].argv, ["msg", "sub", "--json", "--prefix", "lane/"]);
	assert.equal(result.isError, undefined);
	assert.equal(result.details.entries.length, 2);
	assert.equal(result.details.killed, true);
	assert.match(result.content[0].text, /1 lane\/a working/);
});

test("box refuses key and prefix together before any dm call", async (t) => {
	const box = sandbox(t);

	const result = await run(box, "dm_box", { action: "list", key: "lane/a", prefix: "lane/" });

	assert.equal(result.isError, true);
	assert.match(result.content[0].text, /exclusive/);
	assert.deepEqual(box.calls(), []);
});

test("turn ask opens the question set with the tool call id", async (t) => {
	const box = sandbox(t);
	box.resp("exec.ask-open", { id: "a1" });
	const questions = [
		{ question: "Which base?", header: "base", options: [{ label: "main" }, { label: "next" }] },
	];

	const result = await run(box, "dm_turn", { action: "ask", pane: "w1p1", questions });

	const req = { action: "ask-open", target: "w1p1", res: { toolCallId: "toolu_1", questions } };
	assert.deepEqual(box.calls()[0].argv, ["exec", JSON.stringify(req)]);
	assert.equal(result.details.askId, "a1");
	assert.equal(result.content[0].text, "ask a1 opened on w1p1");
});

test("turn answer sends the items and the note", async (t) => {
	const box = sandbox(t);
	box.resp("exec.ask-answer", { items: [{ questionIndex: 0, optionIndexes: [0] }], globalNote: "ship" });

	const result = await run(box, "dm_turn", {
		action: "answer",
		askId: "a1",
		items: [{ questionIndex: 0, optionIndexes: [0] }],
		globalNote: "ship",
	});

	const req = {
		action: "ask-answer",
		target: "a1",
		res: { items: [{ questionIndex: 0, optionIndexes: [0] }], globalNote: "ship", cancel: false },
	};
	assert.deepEqual(box.calls()[0].argv, ["exec", JSON.stringify(req)]);
	assert.equal(result.content[0].text, "ask a1 answered");
});

test("turn await keeps waiting past pending", async (t) => {
	const box = sandbox(t);
	box.resp("ask.await", { pending: true }, 0);
	box.resp("ask.await", { answer: { items: [{ questionIndex: 0, optionIndexes: [1], notes: "small" }] } }, 1);

	const result = await run(box, "dm_turn", { action: "await", askId: "a1", timeout: 60 });

	assert.deepEqual(
		box.calls().map((c) => c.argv),
		[
			["ask", "await", "a1", "--timeout", "25s", "--json"],
			["ask", "await", "a1", "--timeout", "25s", "--json"],
		],
	);
	assert.equal(result.isError, undefined);
	assert.equal(result.details.answer.items[0].notes, "small");
	assert.match(result.content[0].text, /answered: q0 options 1 notes "small"/);
});

test("turn await reports pending once the deadline passes", async (t) => {
	const box = sandbox(t);
	box.resp("ask.await", { pending: true });

	const result = await run(box, "dm_turn", { action: "await", askId: "a1", timeout: 1 });

	assert.equal(result.isError, undefined);
	assert.equal(result.details.pending, true);
	assert.match(result.content[0].text, /pending: nobody answered/);
	assert.equal(box.calls().length, 1);
});

test("a dm refusal surfaces as a tool error with its text", async (t) => {
	const box = sandbox(t);
	box.fail("msg.list", "dm: box: connection refused\n");

	const result = await run(box, "dm_box", { action: "list" });

	assert.equal(result.isError, true);
	assert.match(result.content[0].text, /connection refused/);
	assert.equal(result.details.action, "list");
});
