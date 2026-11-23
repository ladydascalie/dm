/**
 * The bridge against a fake pi and a fake dm on PATH. No terminal, no session,
 * no real store: every dm call is a script that records its argv and stdin and
 * answers from files beside itself.
 */

import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { test } from "node:test";

import registerAskBridge, { DECLINE_MESSAGE, TOOL_NAME } from "./ask-bridge.ts";

type Call = { args: string[]; body: string };
type Tool = {
	execute(
		toolCallId: string,
		params: unknown,
		signal: AbortSignal | undefined,
		onUpdate: unknown,
		ctx: unknown,
	): Promise<{ content: { type: string; text: string }[]; details: Record<string, any>; isError?: boolean }>;
};

const FAKE_DM = `#!/usr/bin/env node
const fs = require("node:fs");
const path = require("node:path");
const dir = process.env.FAKE_DM_DIR;
const args = process.argv.slice(2);
let body = "";
try { body = fs.readFileSync(0, "utf8"); } catch (e) {}
fs.appendFileSync(path.join(dir, "calls.jsonl"), JSON.stringify({ args, body }) + "\\n");
const verb = args[0] === "ask" ? args[1] : args[0] || "";
const fail = path.join(dir, "fail." + verb);
if (fs.existsSync(fail)) {
  process.stderr.write(fs.readFileSync(fail, "utf8"));
  process.exit(1);
}
if (verb === "open") {
  const idFile = path.join(dir, "id");
  const id = fs.existsSync(idFile) ? fs.readFileSync(idFile, "utf8").trim() : "ask-1";
  process.stdout.write(JSON.stringify({ id }) + "\\n");
  process.exit(0);
}
if (verb === "await") {
  const counter = path.join(dir, "awaits");
  const n = fs.existsSync(counter) ? Number(fs.readFileSync(counter, "utf8").trim() || "0") : 0;
  fs.writeFileSync(counter, String(n + 1));
  const seq = path.join(dir, "await." + n);
  const file = fs.existsSync(seq) ? seq : path.join(dir, "await");
  process.stdout.write(fs.readFileSync(file, "utf8"));
  process.exit(0);
}
if (verb === "close") {
  process.stdout.write(JSON.stringify({ closed: true }) + "\\n");
  process.exit(0);
}
process.stderr.write("fake dm: unknown verb " + verb + "\\n");
process.exit(2);
`;

const OPTION_QUESTION = {
	question: "Which base should the branch start from?",
	header: "Base",
	options: [
		{ label: "main", description: "The landed line.", preview: "# main\n\nstable" },
		{ label: "next", description: "The integration line." },
	],
};

type Sandbox = {
	base: string;
	state: string;
	calls: () => Call[];
	writeAwait(data: unknown, index?: number): void;
	writeFail(verb: string, text: string): void;
	setPane(pane: string | undefined): void;
	setPathWithDM(withDM: boolean): void;
	tool: Tool;
};

const saved = {
	PATH: process.env.PATH,
	DM_PANE: process.env.DM_PANE,
	FAKE_DM_DIR: process.env.FAKE_DM_DIR,
};

function restore(): void {
	for (const [key, value] of Object.entries(saved)) {
		if (value === undefined) delete process.env[key];
		else process.env[key] = value;
	}
}

function sandbox(t: { after(fn: () => void): void }): Sandbox {
	const base = fs.mkdtempSync(path.join(os.tmpdir(), "ask-bridge-"));
	const bin = path.join(base, "bin");
	const state = path.join(base, "state");
	fs.mkdirSync(bin);
	fs.mkdirSync(state);
	const dm = path.join(bin, "dm");
	fs.writeFileSync(dm, FAKE_DM);
	fs.chmodSync(dm, 0o755);

	process.env.FAKE_DM_DIR = state;
	process.env.DM_PANE = "w1p1";
	const original = saved.PATH ?? "";
	process.env.PATH = `${bin}:${original}`;

	t.after(() => {
		restore();
		fs.rmSync(base, { recursive: true, force: true });
	});

	let tool: Tool | undefined;
	const pi = {
		registerTool(definition: Tool) {
			tool = definition;
		},
	};
	registerAskBridge(pi as never);

	const callsFile = path.join(state, "calls.jsonl");
	return {
		base,
		state,
		calls() {
			if (!fs.existsSync(callsFile)) return [];
			return fs
				.readFileSync(callsFile, "utf8")
				.split("\n")
				.filter((line) => line.trim() !== "")
				.map((line) => JSON.parse(line) as Call);
		},
		writeAwait(data: unknown, index?: number) {
			const file = index === undefined ? path.join(state, "await") : path.join(state, `await.${index}`);
			fs.writeFileSync(file, `${JSON.stringify(data)}\n`);
		},
		writeFail(verb: string, text: string) {
			fs.writeFileSync(path.join(state, `fail.${verb}`), text);
		},
		setPane(pane: string | undefined) {
			if (pane === undefined) delete process.env.DM_PANE;
			else process.env.DM_PANE = pane;
		},
		setPathWithDM(withDM: boolean) {
			process.env.PATH = withDM ? `${bin}:${original}` : path.join(base, "empty");
			if (!withDM) fs.mkdirSync(path.join(base, "empty"), { recursive: true });
		},
		get tool() {
			assert.ok(tool, "the extension registered no tool");
			return tool;
		},
	};
}

/** A fake pi context: `selects` and `inputs` are the operator's answers, in order. */
function context(selects: (string | undefined)[], inputs: (string | undefined)[] = []) {
	const titles: string[] = [];
	return {
		hasUI: true,
		cwd: process.cwd(),
		titles,
		ui: {
			async select(title: string) {
				titles.push(title);
				return selects.shift();
			},
			async input(title: string) {
				titles.push(title);
				return inputs.shift();
			},
		},
	};
}

test("registers ask_user_question with the agent's parameter shape", (t) => {
	const box = sandbox(t);
	assert.equal(box.tool.name, TOOL_NAME);
	const params = (box.tool as unknown as { parameters: any }).parameters;
	assert.equal(params.properties.questions.maxItems, 4);
	assert.equal(params.properties.questions.items.properties.options.minItems, 2);
	assert.equal(params.properties.questions.items.properties.options.maxItems, 4);
	assert.equal(params.properties.questions.items.properties.header.maxLength, 16);
});

test("opens, awaits and closes the store, then returns rpiv's envelope", async (t) => {
	const box = sandbox(t);
	box.writeAwait({ answer: { items: [{ questionIndex: 0, optionIndexes: [0] }] } });

	const result = await box.tool.execute("toolu_1", { questions: [OPTION_QUESTION] }, undefined, undefined, context([]));

	assert.equal(
		result.content[0].text,
		'User has answered your questions: "Which base should the branch start from?"="main". selected preview: # main\n\nstable. You can now continue with the user\'s answers in mind.',
	);
	assert.equal(result.details.path, "dm");
	assert.equal(result.details.cancelled, false);
	assert.equal(result.details.askId, "ask-1");
	assert.deepEqual(result.details.answers, [
		{
			questionIndex: 0,
			question: "Which base should the branch start from?",
			kind: "option",
			answer: "main",
			preview: "# main\n\nstable",
		},
	]);

	const calls = box.calls();
	assert.deepEqual(
		calls.map((c) => c.args),
		[
			["ask", "open", "--pane", "w1p1", "--tool-call", "toolu_1", "--json"],
			["ask", "await", "ask-1", "--json"],
			["ask", "close", "ask-1", "--json"],
		],
	);
	assert.deepEqual(JSON.parse(calls[0].body), {
		questions: [
			{
				question: "Which base should the branch start from?",
				header: "Base",
				multiSelect: false,
				options: [
					{ label: "main", description: "The landed line.", preview: "# main\n\nstable" },
					{ label: "next", description: "The integration line." },
				],
			},
		],
	});
});

test("keeps awaiting while the store says pending", async (t) => {
	const box = sandbox(t);
	box.writeAwait({ pending: true }, 0);
	box.writeAwait({ answer: { items: [{ questionIndex: 0, optionIndexes: [1] }] } }, 1);

	const result = await box.tool.execute("toolu_2", { questions: [OPTION_QUESTION] }, undefined, undefined, context([]));

	assert.equal(
		result.content[0].text,
		'User has answered your questions: "Which base should the branch start from?"="next". You can now continue with the user\'s answers in mind.',
	);
	const awaits = box.calls().filter((c) => c.args[1] === "await");
	assert.equal(awaits.length, 2);
});

test("maps a multi-select answer in authored order", async (t) => {
	const box = sandbox(t);
	const question = {
		question: "Which features do you want?",
		header: "Features",
		multiSelect: true,
		options: [
			{ label: "Alpha", description: "One." },
			{ label: "Beta", description: "Two." },
			{ label: "Gamma", description: "Three." },
		],
	};
	box.writeAwait({ answer: { items: [{ questionIndex: 0, optionIndexes: [2, 0], text: "plus text" }] } });

	const result = await box.tool.execute("toolu_3", { questions: [question] }, undefined, undefined, context([]));

	assert.equal(
		result.content[0].text,
		'User has answered your questions: "Which features do you want?"="Alpha, Gamma, plus text". You can now continue with the user\'s answers in mind.',
	);
	assert.deepEqual(result.details.answers, [
		{
			questionIndex: 0,
			question: "Which features do you want?",
			kind: "multi",
			answer: "plus text",
			selected: ["Alpha", "Gamma"],
		},
	]);
});

test("carries a note and a global note through the envelope", async (t) => {
	const box = sandbox(t);
	box.writeAwait({
		answer: {
			items: [{ questionIndex: 0, optionIndexes: [1], notes: "  keep it small  " }],
			globalNote: "ship on friday",
		},
	});

	const result = await box.tool.execute("toolu_4", { questions: [OPTION_QUESTION] }, undefined, undefined, context([]));

	assert.equal(
		result.content[0].text,
		'User has answered your questions: "Which base should the branch start from?"="next". user notes: keep it small. global note: ship on friday. You can now continue with the user\'s answers in mind.',
	);
	assert.equal(result.details.globalNote, "ship on friday");
	assert.equal(result.details.answers[0].notes, "keep it small");
});

test("declines with the canonical string when the answer cancels", async (t) => {
	const box = sandbox(t);
	box.writeAwait({ answer: { cancel: true } });

	const result = await box.tool.execute("toolu_5", { questions: [OPTION_QUESTION] }, undefined, undefined, context([]));

	assert.equal(result.content[0].text, DECLINE_MESSAGE);
	assert.equal(result.details.cancelled, true);
	assert.equal(result.details.path, "dm");
	const verbs = box.calls().map((c) => c.args[1]);
	assert.deepEqual(verbs, ["open", "await", "close"]);
});

test("passes a daemon refusal through as a tool error with its code", async (t) => {
	const box = sandbox(t);
	box.writeFail("open", "dm: ask: E_EXISTS: tool call toolu_6 already has an ask\n");

	const result = await box.tool.execute("toolu_6", { questions: [OPTION_QUESTION] }, undefined, undefined, context([]));

	assert.equal(result.isError, true);
	assert.match(result.content[0].text, /E_EXISTS/);
	assert.equal(result.details.error, "E_EXISTS");
	assert.deepEqual(box.calls().map((c) => c.args[1]), ["open"]);
});

test("falls back to the terminal dialogs with no DM_PANE", async (t) => {
	const box = sandbox(t);
	box.setPane(undefined);
	const ctx = context(["next"]);

	const result = await box.tool.execute("toolu_7", { questions: [OPTION_QUESTION] }, undefined, undefined, ctx);

	assert.equal(
		result.content[0].text,
		'User has answered your questions: "Which base should the branch start from?"="next". You can now continue with the user\'s answers in mind.',
	);
	assert.equal(result.details.path, "tui");
	assert.equal(result.details.fallbackReason, "no DM_PANE");
	assert.deepEqual(result.details.answers, [
		{
			questionIndex: 0,
			question: "Which base should the branch start from?",
			kind: "option",
			answer: "next",
		},
	]);
	assert.deepEqual(box.calls(), []);
	assert.match(ctx.titles[0], /Which base should the branch start from\?/);
});

test("falls back when dm is not on PATH", async (t) => {
	const box = sandbox(t);
	box.setPathWithDM(false);

	const result = await box.tool.execute("toolu_8", { questions: [OPTION_QUESTION] }, undefined, undefined, context(["main"]));

	assert.equal(result.details.path, "tui");
	assert.match(String(result.details.fallbackReason), /PATH/);
});

test("falls back when the daemon is unreachable", async (t) => {
	const box = sandbox(t);
	box.writeFail("open", "dm: dial unix /run/user/1000/dm/main.sock: connect: no such file or directory\n");

	const result = await box.tool.execute("toolu_9", { questions: [OPTION_QUESTION] }, undefined, undefined, context(["main"]));

	assert.equal(result.details.path, "tui");
	assert.match(String(result.details.fallbackReason), /unreachable/);
	assert.equal(result.isError, undefined);
});

test("walks a multi-select and a typed answer in the fallback", async (t) => {
	const box = sandbox(t);
	box.setPane(undefined);
	const question = {
		question: "Which features do you want?",
		header: "Features",
		multiSelect: true,
		options: [
			{ label: "Alpha", description: "One." },
			{ label: "Beta", description: "Two." },
		],
	};
	const ctx = context(["Beta", "Done choosing."]);

	const result = await box.tool.execute("toolu_10", { questions: [question] }, undefined, undefined, ctx);

	assert.equal(
		result.content[0].text,
		'User has answered your questions: "Which features do you want?"="Beta". You can now continue with the user\'s answers in mind.',
	);
	assert.deepEqual(result.details.answers[0].selected, ["Beta"]);

	const typed = context(["Type something."], ["only the small one"]);
	const second = await box.tool.execute("toolu_11", { questions: [OPTION_QUESTION] }, undefined, undefined, typed);
	assert.equal(
		second.content[0].text,
		'User has answered your questions: "Which base should the branch start from?"="only the small one". You can now continue with the user\'s answers in mind.',
	);
	assert.equal(second.details.answers[0].kind, "custom");
});

test("declines the whole ask when the fallback is escaped", async (t) => {
	const box = sandbox(t);
	box.setPane(undefined);
	const two = {
		...OPTION_QUESTION,
	};
	const second = {
		question: "And the next one?",
		header: "Next",
		options: [
			{ label: "yes", description: "Go." },
			{ label: "no", description: "Stop." },
		],
	};
	const ctx = context(["main", undefined]);

	const result = await box.tool.execute(
		"toolu_12",
		{ questions: [two, second] },
		undefined,
		undefined,
		ctx,
	);

	assert.equal(result.content[0].text, DECLINE_MESSAGE);
	assert.equal(result.details.cancelled, true);
});

test("rejects an ask that breaks the caps before any dm call", async (t) => {
	const box = sandbox(t);
	const questions = Array.from({ length: 5 }, (_v, i) => ({
		question: `Question ${i}?`,
		header: "Q",
		options: [
			{ label: "a", description: "A." },
			{ label: "b", description: "B." },
		],
	}));

	const result = await box.tool.execute("toolu_13", { questions }, undefined, undefined, context([]));

	assert.equal(result.content[0].text, "Error: At most 4 questions are allowed per invocation");
	assert.equal(result.details.error, "too_many_questions");
	assert.equal(result.details.cancelled, true);
	assert.deepEqual(box.calls(), []);
});

test("rejects a reserved option label and one option short", async (t) => {
	const box = sandbox(t);
	const reserved = await box.tool.execute(
		"toolu_14",
		{
			questions: [
				{
					question: "Pick?",
					header: "Pick",
					options: [
						{ label: "Other", description: "Nope." },
						{ label: "fine", description: "Yes." },
					],
				},
			],
		},
		undefined,
		undefined,
		context([]),
	);
	assert.equal(reserved.content[0].text, "Error: Option label is reserved (Other, Type something., Next)");
	assert.equal(reserved.details.error, "reserved_label");

	const short = await box.tool.execute(
		"toolu_15",
		{ questions: [{ question: "Pick?", header: "Pick", options: [{ label: "one", description: "One." }] }] },
		undefined,
		undefined,
		context([]),
	);
	assert.equal(short.content[0].text, "Error: Each question requires at least 2 options");
	assert.equal(short.details.error, "empty_options");
	assert.deepEqual(box.calls(), []);
});

test("refuses without UI when there is no store and no terminal", async (t) => {
	const box = sandbox(t);
	box.setPane(undefined);
	const ctx = context([]);
	(ctx as { hasUI: boolean }).hasUI = false;

	const result = await box.tool.execute("toolu_16", { questions: [OPTION_QUESTION] }, undefined, undefined, ctx);

	assert.equal(result.isError, true);
	assert.match(result.content[0].text, /UI not available/);
	assert.equal(result.details.error, "no_ui");
});
