/**
 * ask-bridge: the pi side of dm's ask store.
 *
 * A pi extension that registers `ask_user_question` and answers it with dm's
 * own store, so the question lands in the session state every dm surface
 * reads: the client float, the web form, another pane. Nothing here paints.
 *
 * One call runs: `dm ask open` with the questions on stdin, `dm ask await`
 * until the store carries an answer or a cancel, then `dm ask close`. The
 * reply is the envelope agents already read, copied from
 * rpiv-ask-user-question's tool schema so a prompt that mentions this tool
 * keeps its meaning.
 *
 * A pi outside a dm pane (no DM_PANE), or without dm on PATH, falls back to
 * the plain select and input dialogs, so a bare terminal still asks. The
 * details name the path that ran.
 *
 * No runtime import of pi's packages: the test drives the file with plain
 * node, and only pi's own loader needs the extension API type.
 */

import { spawn } from "node:child_process";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

export const TOOL_NAME = "ask_user_question";

/** The decline text is the model's one canonical "did not answer" signal. */
export const DECLINE_MESSAGE = "User declined to answer questions";

const ENVELOPE_PREFIX = "User has answered your questions:";
const ENVELOPE_SUFFIX = "You can now continue with the user's answers in mind.";
/** What rpiv prints for an answered question with no text. */
const NO_INPUT = "(no input)";

const MAX_QUESTIONS = 4;
const MIN_OPTIONS = 2;
const MAX_OPTIONS = 4;
const RESERVED_LABELS = ["Other", "Type something.", "Next"];
/** The row the dialog appends on every question; picking it opens a text input. */
const CUSTOM_ROW = "Type something.";
/** The fallback's own row: a multi-select loop needs a way out. */
const DONE_ROW = "Done choosing.";

const OPEN_TIMEOUT_MS = 30_000;
/** Above the daemon's own wait bound, so one await normally ends in a reply. */
const AWAIT_TIMEOUT_MS = 120_000;
const CLOSE_TIMEOUT_MS = 15_000;

export type Option = { label: string; description?: string; preview?: string };

export type Question = {
	question: string;
	header: string;
	multiSelect?: boolean;
	options: Option[];
};

/** One question's answer, shaped the way rpiv's dialog shapes it. */
export type AnswerView = {
	questionIndex: number;
	question: string;
	kind: "option" | "custom" | "multi";
	answer: string | null;
	selected?: string[];
	notes?: string;
	preview?: string;
};

export type Result = {
	answers: AnswerView[];
	cancelled: boolean;
	globalNote?: string;
	unanswered?: number[];
};

/** The dm answer the CLI prints at ask-await, reduced to what the bridge reads. */
type DMAnswer = {
	cancel?: boolean;
	items?: DMItem[];
	globalNote?: string;
};

type DMItem = {
	questionIndex: number;
	optionIndexes?: number[];
	text?: string;
	notes?: string;
};

type RunResult = { code: number; stdout: string; stderr: string; missing: boolean };

type Invalid = { code: string; message: string };

/**
 * The parameter schema. It keeps the name and shape agents already write, and
 * the caps hold here, at the schema, the way rpiv's TypeBox schema holds them.
 * A plain literal is TypeBox-compatible, and it keeps node's box out of this
 * file.
 */
const parameters = {
	type: "object",
	properties: {
		questions: {
			type: "array",
			minItems: 1,
			maxItems: MAX_QUESTIONS,
			description: "The questions to put to the operator, 1 to 4.",
			items: {
				type: "object",
				properties: {
					question: {
						type: "string",
						description:
							'The complete question. Clear, specific, and ending with a question mark, e.g. "Which base should the branch start from?"',
					},
					header: {
						type: "string",
						maxLength: 16,
						description: "A chip label of 16 characters or fewer, e.g. \"Base\" or \"Auth method\".",
					},
					multiSelect: {
						type: "boolean",
						default: false,
						description: "True when several options may be picked. Phrase the question accordingly.",
					},
					options: {
						type: "array",
						minItems: MIN_OPTIONS,
						maxItems: MAX_OPTIONS,
						description:
							"2 to 4 choices. Do not author the \"Type something.\" row; it is appended for you.",
						items: {
							type: "object",
							properties: {
								label: {
									type: "string",
									maxLength: 60,
									description: "The choice text, 1 to 5 words and 60 characters or fewer.",
								},
								description: {
									type: "string",
									description: "What the choice means, or its trade-off.",
								},
								preview: {
									type: "string",
									description:
										"Optional markdown shown beside the options: a mockup, a snippet, a config. Single-select only.",
								},
							},
							required: ["label", "description"],
							additionalProperties: false,
						},
					},
				},
				required: ["question", "header", "options"],
				additionalProperties: false,
			},
		},
	},
	required: ["questions"],
	additionalProperties: false,
};

const DESCRIPTION = `Ask the operator one or more structured questions, a decision the work cannot proceed without.
1. Gather preferences or requirements
2. Clarify an ambiguous instruction
3. Get a decision on an implementation choice
4. Offer directions the work could take

Usage notes:
- The questions land in dm's session state and are drawn by dm's client, the web page, or the operator's own pi terminal. Escape declines the whole ask from every surface.
- Users can type a custom answer via the automatically appended "Type something." row on every question. Do NOT author "Other" or "Type something." labels yourself: reserved labels are rejected.
- Use multiSelect: true when several answers are valid.
- If you recommend an option, put it first and add "(Recommended)" to its label.
- Use options[].preview for artifacts the operator must compare: ASCII mockups, code snippets, configs. Single-select only.`;

const PROMPT_SNIPPET = `Ask the operator up to ${MAX_QUESTIONS} structured questions (${MIN_OPTIONS}-${MAX_OPTIONS} options each) when requirements are ambiguous`;

const PROMPT_GUIDELINES = [
	`Use ${TOOL_NAME} whenever the user's request is underspecified and you cannot proceed without concrete decisions. You can ask up to ${MAX_QUESTIONS} questions per invocation.`,
	`Each ${TOOL_NAME} question MUST have ${MIN_OPTIONS}-${MAX_OPTIONS} options. Every option requires a concise label (1-5 words) and a description explaining what the choice means or its trade-offs. The operator can type a custom answer via the automatically appended "Type something." row, or decline the ask. Do NOT author "Other" or "Type something." labels yourself: reserved labels are rejected.`,
	`Set multiSelect: true in ${TOOL_NAME} when multiple answers are valid. Provide options[].preview when an option benefits from richer side-by-side context (mockups, code snippets, diagrams, configs).`,
	`Do not stack multiple ${TOOL_NAME} calls back-to-back. Group all clarifying questions into one invocation.`,
];

/** The ask-open and ask-answer wire is dm's, so the shape is drawn explicitly. */
function wireQuestions(questions: Question[]) {
	return questions.map((q) => ({
		question: q.question,
		header: q.header,
		multiSelect: Boolean(q.multiSelect),
		options: q.options.map((o) => ({
			label: o.label,
			description: o.description ?? "",
			...(o.preview ? { preview: o.preview } : {}),
		})),
	}));
}

/**
 * The rules rpiv's runtime validator applies, with its messages: the schema
 * caps are the schema's job, and these are the ones a call can still break.
 */
export function validateQuestions(questions: Question[]): Invalid | undefined {
	if (questions.length === 0) {
		return { code: "no_questions", message: "Error: At least one question is required" };
	}
	if (questions.length > MAX_QUESTIONS) {
		return {
			code: "too_many_questions",
			message: `Error: At most ${MAX_QUESTIONS} questions are allowed per invocation`,
		};
	}
	const seen = new Set<string>();
	for (const q of questions) {
		if (seen.has(q.question)) {
			return { code: "duplicate_question", message: "Error: Question text must be unique within an invocation" };
		}
		seen.add(q.question);
	}
	for (const q of questions) {
		if (!Array.isArray(q.options) || q.options.length < MIN_OPTIONS) {
			return {
				code: "empty_options",
				message: `Error: Each question requires at least ${MIN_OPTIONS} options`,
			};
		}
		if (q.options.length > MAX_OPTIONS) {
			return {
				code: "too_many_options",
				message: `Error: Each question allows at most ${MAX_OPTIONS} options`,
			};
		}
		const labels = new Set<string>();
		for (const o of q.options) {
			if (RESERVED_LABELS.includes(o.label)) {
				return {
					code: "reserved_label",
					message: `Error: Option label is reserved (${RESERVED_LABELS.join(", ")})`,
				};
			}
			if (labels.has(o.label)) {
				return { code: "duplicate_option_label", message: "Error: Option labels must be unique within a question" };
			}
			labels.add(o.label);
		}
	}
	return undefined;
}

/** The scalar an answer prints as inside the envelope, word for word rpiv's. */
function scalar(a: AnswerView): string {
	switch (a.kind) {
		case "multi": {
			const values = [...(a.selected ?? [])];
			if (a.answer && a.answer.length > 0) values.push(a.answer);
			return values.length > 0 ? values.join(", ") : NO_INPUT;
		}
		case "custom":
			return a.answer && a.answer.length > 0 ? a.answer : NO_INPUT;
		case "option":
			return a.answer ?? NO_INPUT;
	}
}

function answerSegment(a: AnswerView): string {
	const parts = [`"${a.question}"="${scalar(a)}"`];
	if (a.preview && a.preview.length > 0) parts.push(`selected preview: ${a.preview}`);
	if (a.notes && a.notes.length > 0) parts.push(`user notes: ${a.notes}`);
	return `${parts.join(". ")}.`;
}

function unansweredSegment(indexes: number[], questions: Question[]): string {
	const names = indexes.map((i) => `"${questions[i]?.question}"`);
	return `unanswered: ${names.join(", ")}.`;
}

/**
 * Build the model-facing envelope and the details rpiv would have returned.
 * A cancelled result, and a result with neither answers nor a global note,
 * both collapse to the one decline string.
 */
export function buildResponse(questions: Question[], result: Result): Result & { text: string } {
	if (result.cancelled) {
		return {
			answers: result.answers,
			cancelled: true,
			...(result.globalNote ? { globalNote: result.globalNote } : {}),
			text: DECLINE_MESSAGE,
		};
	}
	const segments: string[] = [];
	const unanswered: number[] = [];
	for (let i = 0; i < questions.length; i++) {
		const a = result.answers.find((x) => x.questionIndex === i);
		if (a) segments.push(answerSegment(a));
		else unanswered.push(i);
	}
	const note = result.globalNote && result.globalNote.length > 0 ? result.globalNote : undefined;
	if (segments.length === 0 && !note) {
		return { answers: result.answers, cancelled: true, text: DECLINE_MESSAGE };
	}
	if (unanswered.length > 0) segments.push(unansweredSegment(unanswered, questions));
	if (note) segments.push(`global note: ${note}.`);
	return {
		answers: result.answers,
		cancelled: false,
		...(note ? { globalNote: note } : {}),
		...(unanswered.length > 0 ? { unanswered } : {}),
		text: `${ENVELOPE_PREFIX} ${segments.join(" ")} ${ENVELOPE_SUFFIX}`,
	};
}

function trimNote(value: string | undefined): string | undefined {
	const trimmed = value?.trim();
	return trimmed ? trimmed : undefined;
}

/** The store's answer, read the way the dialog would have recorded it. */
export function viewOf(questions: Question[], answer: DMAnswer): Result {
	const note = trimNote(answer.globalNote);
	if (answer.cancel) {
		return { answers: [], cancelled: true, ...(note ? { globalNote: note } : {}) };
	}
	const answers: AnswerView[] = [];
	for (const item of answer.items ?? []) {
		const q = questions[item.questionIndex];
		if (!q) continue;
		const picked = item.optionIndexes ?? [];
		const notes = trimNote(item.notes);
		const base = {
			questionIndex: item.questionIndex,
			question: q.question,
			...(notes ? { notes } : {}),
		};
		if (q.multiSelect) {
			const selected = q.options.filter((_o, i) => picked.includes(i)).map((o) => o.label);
			answers.push({ ...base, kind: "multi", answer: item.text ? item.text : null, selected });
			continue;
		}
		if (item.text) {
			answers.push({ ...base, kind: "custom", answer: item.text });
			continue;
		}
		const option = q.options[picked[0] ?? -1];
		answers.push({
			...base,
			kind: "option",
			answer: option?.label ?? null,
			...(option?.preview ? { preview: option.preview } : {}),
		});
	}
	answers.sort((a, b) => a.questionIndex - b.questionIndex);
	return { answers, cancelled: false, ...(note ? { globalNote: note } : {}) };
}

/**
 * Run one dm command without a shell. The body, when there is one, goes to
 * stdin: the question set is too big for argv. A spawn that cannot find dm
 * says so with `missing`, so the caller can fall back instead of reporting a
 * broken install.
 */
function runDM(
	args: string[],
	body: string | undefined,
	opts: { signal?: AbortSignal; timeoutMs?: number } = {},
): Promise<RunResult> {
	return new Promise((resolve) => {
		let child: ReturnType<typeof spawn>;
		try {
			child = spawn("dm", args, { signal: opts.signal, stdio: ["pipe", "pipe", "pipe"] });
		} catch (err) {
			resolve({ code: -1, stdout: "", stderr: err instanceof Error ? err.message : String(err), missing: true });
			return;
		}
		let stdout = "";
		let stderr = "";
		let missing = false;
		let settled = false;
		let timer: ReturnType<typeof setTimeout> | undefined;
		const finish = (code: number) => {
			if (settled) return;
			settled = true;
			if (timer) clearTimeout(timer);
			resolve({ code, stdout, stderr, missing });
		};
		if (opts.timeoutMs) {
			timer = setTimeout(() => {
				stderr += `${stderr ? " " : ""}dm ${args.join(" ")} timed out after ${opts.timeoutMs}ms`;
				child.kill("SIGKILL");
			}, opts.timeoutMs);
			timer.unref?.();
		}

		child.stdout?.on("data", (chunk: Buffer) => {
			stdout += chunk.toString();
		});
		child.stderr?.on("data", (chunk: Buffer) => {
			stderr += chunk.toString();
		});
		child.on("error", (err: NodeJS.ErrnoException) => {
			if (err.code === "ENOENT") missing = true;
			stderr += `${stderr ? "\n" : ""}${err.message}`;
			if (missing) finish(-1);
		});
		child.on("close", (code) => finish(code ?? -1));

		child.stdin?.on("error", () => {
			// A command that exits before reading its body is not a bridge fault.
		});
		if (body !== undefined) child.stdin?.write(body);
		child.stdin?.end();
	});
}

/** dm prints refusals as "dm: ask: <code>: <msg>", so the code rides the text. */
function refusalCode(text: string): string | undefined {
	const match = /ask: ([A-Za-z_]+): /.exec(text);
	return match?.[1];
}

function sleep(ms: number): Promise<void> {
	return new Promise((resolve) => setTimeout(resolve, ms));
}

type ToolResult = {
	content: { type: "text"; text: string }[];
	details: Record<string, unknown>;
	isError?: boolean;
};

function detailsOf(result: Result, path: "dm" | "tui", extra: Record<string, unknown> = {}) {
	return {
		answers: result.answers,
		cancelled: result.cancelled,
		...(result.globalNote ? { globalNote: result.globalNote } : {}),
		...(result.unanswered ? { unanswered: result.unanswered } : {}),
		path,
		...extra,
	};
}

function validationResult(invalid: Invalid): ToolResult {
	return {
		content: [{ type: "text", text: invalid.message }],
		details: { answers: [], cancelled: true, error: invalid.code, path: "none" },
	};
}

function toolError(text: string, details: Record<string, unknown> = {}): ToolResult {
	return {
		content: [{ type: "text", text }],
		details: { answers: [], cancelled: true, ...details },
		isError: true,
	};
}

/** An interrupted call reads as a decline, the same as Esc in the dialog. */
function declinedResult(path: "dm" | "tui", extra: Record<string, unknown> = {}): ToolResult {
	return {
		content: [{ type: "text", text: DECLINE_MESSAGE }],
		details: detailsOf({ answers: [], cancelled: true }, path, extra),
	};
}

/** Walk one question with the terminal dialogs; undefined means the operator declined. */
async function askOne(
	ui: { select(title: string, options: string[]): Promise<string | undefined>; input(title: string, placeholder?: string): Promise<string | undefined> },
	q: Question,
	index: number,
): Promise<AnswerView | undefined> {
	const title = q.header ? `${q.header}: ${q.question}` : q.question;
	if (!q.multiSelect) {
		const labels = q.options.map((o) => o.label);
		const pick = await ui.select(title, [...labels, CUSTOM_ROW]);
		if (pick === undefined || pick === null) return undefined;
		const at = labels.indexOf(pick);
		if (at < 0) {
			const text = await askText(ui, q.question);
			if (text === undefined) return undefined;
			return { questionIndex: index, question: q.question, kind: "custom", answer: text };
		}
		const option = q.options[at];
		return {
			questionIndex: index,
			question: q.question,
			kind: "option",
			answer: option.label,
			...(option.preview ? { preview: option.preview } : {}),
		};
	}

	const picked: number[] = [];
	let text: string | undefined;
	for (;;) {
		const remaining = q.options.map((_o, i) => i).filter((i) => !picked.includes(i));
		const labels = remaining.map((i) => q.options[i].label);
		const rows = [...labels, CUSTOM_ROW, ...(picked.length > 0 ? [DONE_ROW] : [])];
		const prompt =
			picked.length > 0
				? `${title} (picked: ${q.options.filter((_o, i) => picked.includes(i)).map((o) => o.label).join(", ")})`
				: title;
		const pick = await ui.select(prompt, rows);
		if (pick === undefined || pick === null) return undefined;
		if (pick === DONE_ROW) break;
		if (pick === CUSTOM_ROW) {
			const typed = await askText(ui, q.question);
			if (typed === undefined) return undefined;
			text = typed;
			break;
		}
		const at = labels.indexOf(pick);
		if (at < 0) continue;
		picked.push(remaining[at]);
	}
	const selected = q.options.filter((_o, i) => picked.includes(i)).map((o) => o.label);
	return { questionIndex: index, question: q.question, kind: "multi", answer: text ?? null, selected };
}

/** Ask for free text until it is non-blank or the operator gives up. */
async function askText(
	ui: { input(title: string, placeholder?: string): Promise<string | undefined> },
	question: string,
): Promise<string | undefined> {
	for (;;) {
		const text = await ui.input(question, "Type your answer");
		if (text === undefined || text === null) return undefined;
		const trimmed = String(text).trim();
		if (trimmed) return trimmed;
	}
}

/** The plain-terminal path: one dialog walk, no store, no float. */
async function walkQuestions(
	ui: Parameters<typeof askOne>[0],
	questions: Question[],
): Promise<Result> {
	const answers: AnswerView[] = [];
	for (let i = 0; i < questions.length; i++) {
		const answer = await askOne(ui, questions[i], i);
		if (!answer) return { answers: [], cancelled: true };
		answers.push(answer);
	}
	return { answers, cancelled: false };
}

export default function (pi: ExtensionAPI) {
	pi.registerTool({
		name: TOOL_NAME,
		label: "Ask User Question",
		description: DESCRIPTION,
		promptSnippet: PROMPT_SNIPPET,
		promptGuidelines: PROMPT_GUIDELINES,
		// Interactive: always a top-level call, never from a codemode script.
		exposure: "model-only",
		parameters,
		async execute(
			toolCallId: string,
			params: { questions?: Question[] },
			signal: AbortSignal | undefined,
			_onUpdate: unknown,
			ctx: {
				hasUI?: boolean;
				ui?: Parameters<typeof askOne>[0];
			},
		): Promise<ToolResult> {
			const questions = Array.isArray(params?.questions) ? params.questions : [];
			const invalid = validateQuestions(questions);
			if (invalid) return validationResult(invalid);

			const ui = ctx?.ui;
			const canAsk =
				ctx?.hasUI !== false && typeof ui?.select === "function" && typeof ui?.input === "function";
			const fallback = async (reason: string): Promise<ToolResult> => {
				if (!canAsk || !ui) {
					return toolError(
						`Error: UI not available (running in non-interactive mode) and the dm ask store is unreachable (${reason})`,
						{ path: "none", error: "no_ui" },
					);
				}
				const walked = await walkQuestions(ui, questions);
				const built = buildResponse(questions, walked);
				return {
					content: [{ type: "text", text: built.text }],
					details: detailsOf(built, "tui", { fallbackReason: reason }),
				};
			};

			const pane = process.env.DM_PANE;
			if (!pane) return fallback("no DM_PANE");

			const opened = await runDM(
				["ask", "open", "--pane", pane, "--tool-call", toolCallId, "--json"],
				JSON.stringify({ questions: wireQuestions(questions) }),
				{ signal, timeoutMs: OPEN_TIMEOUT_MS },
			);
			if (opened.missing) return fallback("dm is not on PATH");
			if (opened.code !== 0) {
				// An interrupt kills the child, so it lands here. It is not a
				// broken install, and it must not fall through to a terminal ask.
				if (signal?.aborted) return declinedResult("dm");
				const text = (opened.stderr || opened.stdout).trim();
				const code = refusalCode(text);
				if (code) return toolError(text, { path: "none", error: code });
				return fallback(`the dm daemon is unreachable (${text || `exit ${opened.code}`})`);
			}
			let askId = "";
			try {
				askId = String((JSON.parse(opened.stdout) as { id?: string }).id ?? "");
			} catch {
				askId = "";
			}
			if (!askId) return toolError(`dm ask open returned no id: ${opened.stdout.trim()}`, { path: "none" });

			// Closing is idempotent, so a close that finds nothing still exits zero.
			const close = async () => {
				await runDM(["ask", "close", askId, "--json"], undefined, { timeoutMs: CLOSE_TIMEOUT_MS });
			};

			let answer: DMAnswer | undefined;
			for (;;) {
				if (signal?.aborted) {
					await close();
					return declinedResult("dm", { askId });
				}
				const waited = await runDM(["ask", "await", askId, "--json"], undefined, {
					signal,
					timeoutMs: AWAIT_TIMEOUT_MS,
				});
				if (waited.missing) {
					await close();
					return fallback("dm left PATH while the ask was open");
				}
				if (waited.code !== 0) {
					const text = (waited.stderr || waited.stdout).trim();
					const code = refusalCode(text);
					await close();
					if (signal?.aborted) return declinedResult("dm", { askId });
					if (code) return toolError(text, { path: "none", error: code });
					return fallback(`the dm daemon is unreachable (${text || `exit ${waited.code}`})`);
				}
				let reply: { pending?: boolean; answer?: DMAnswer };
				try {
					reply = JSON.parse(waited.stdout) as { pending?: boolean; answer?: DMAnswer };
				} catch {
					await close();
					return toolError(`dm ask await returned no JSON: ${waited.stdout.trim()}`, { path: "none" });
				}
				if (reply.pending) {
					// The daemon parks for its own bound, so a fast pending reply
					// would spin this loop. Give it room.
					await sleep(150);
					continue;
				}
				if (!reply.answer) {
					await close();
					return toolError("dm ask await carried neither pending nor an answer", { path: "none" });
				}
				answer = reply.answer;
				break;
			}

			await close();
			const built = buildResponse(questions, viewOf(questions, answer));
			return {
				content: [{ type: "text", text: built.text }],
				details: detailsOf(built, "dm", { askId }),
			};
		},
	});
}
