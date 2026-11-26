/**
 * The fleet skill is a teaching document, so its test is a gate on the two
 * ways it rots: frontmatter that a harness will not load, and a verb the
 * binary does not have. The command table in internal/clidoc is the authority
 * for the second, so the prose is scanned against it.
 *
 * The prose scan reads the whole file. A bare "dm" is the binary, and only
 * "dm" followed by a verb counts as a citation. Keep nouns away from that
 * pattern: write "in dm's panes", not "in dm panes".
 */

import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as path from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const skillPath = path.join(here, "SKILL.md");
const clidocPath = path.join(here, "..", "..", "internal", "clidoc", "clidoc.go");
const MAX_DESCRIPTION = 1024;
const MAX_NAME = 64;

type Frontmatter = Record<string, string>;

function unquote(value: string): string {
	const quoted = /^"(.*)"$/s.exec(value) ?? /^'(.*)'$/s.exec(value);
	if (quoted) return quoted[1].replace(/''/g, "'");
	return value;
}

function readFrontmatter(text: string): { data: Frontmatter; body: string } {
	const match = /^---\r?\n([\s\S]*?)\r?\n---[ \t]*\r?\n?/.exec(text);
	assert.ok(match, "SKILL.md must open with a frontmatter block");
	const data: Frontmatter = {};
	for (const raw of match[1].split(/\r?\n/)) {
		const line = raw.trim();
		if (line === "") continue;
		const pair = /^([a-z][a-z0-9-]*):[ \t]*(.*)$/.exec(line);
		assert.ok(pair, `frontmatter line does not parse: ${JSON.stringify(line)}`);
		data[pair[1]] = unquote(pair[2].trim());
	}
	return { data, body: text.slice(match[0].length) };
}

/** Every command row in the table. Aliases are not commands, so they are out. */
function readCommands(): Set<string> {
	const source = fs.readFileSync(clidocPath, "utf8");
	const names = new Set<string>();
	for (const match of source.matchAll(/^[ \t]*name:[ \t]*"([^"]*)",[ \t]*$/gm)) {
		if (match[1] !== "") names.add(match[1]);
	}
	assert.ok(names.has("msg post"), "the command table did not parse");
	assert.ok(names.has("llms"), "the command table did not parse");
	return names;
}

/**
 * The verbs the prose cites. A two-word command wins over its first word, so
 * "dm msg wait" is one citation and not "msg" plus a stray "wait".
 */
function citedVerbs(text: string, commands: Set<string>): string[] {
	const verbs: string[] = [];
	for (const match of text.matchAll(/\bdm[ \t]+([a-z][a-z0-9-]*)/g)) {
		const first = match[1];
		const rest = text.slice((match.index ?? 0) + match[0].length);
		const second = /^[ \t]+([a-z][a-z0-9-]*)/.exec(rest)?.[1];
		if (second !== undefined && commands.has(`${first} ${second}`)) verbs.push(`${first} ${second}`);
		else verbs.push(first);
	}
	return verbs;
}

const text = fs.readFileSync(skillPath, "utf8");

test("the frontmatter parses into a name and a routing description", () => {
	const { data } = readFrontmatter(text);
	assert.equal(typeof data.name, "string");
	assert.equal(typeof data.description, "string");
	assert.notEqual(data.name, "", "name is empty");
	assert.notEqual(data.description, "", "description is empty");
});

test("the name matches the directory and the portable rules", () => {
	const { data } = readFrontmatter(text);
	const directory = path.basename(here);
	assert.equal(data.name, directory, "name must match the skill directory");
	assert.match(data.name, /^[a-z0-9]+(-[a-z0-9]+)*$/, "name is lowercase, hyphenated, no empty run");
	assert.ok(data.name.length <= MAX_NAME, `name is ${data.name.length} characters, cap ${MAX_NAME}`);
});

test("the description fits the limit and says when to load it", () => {
	const { data } = readFrontmatter(text);
	const length = [...data.description].length;
	assert.ok(length > 0, "description is empty");
	assert.ok(length <= MAX_DESCRIPTION, `description is ${length} characters, cap ${MAX_DESCRIPTION}`);
	assert.match(data.description, /\bUse when\b/, "description must name when the skill applies");
});

test("every dm subcommand the prose cites exists in internal/clidoc", () => {
	const commands = readCommands();
	const { body } = readFrontmatter(text);
	const verbs = citedVerbs(body, commands);
	assert.ok(verbs.length >= 5, `the prose cites ${verbs.length} commands, too few to prove the scan`);
	for (const verb of verbs) {
		assert.ok(commands.has(verb), `the prose cites "dm ${verb}", which internal/clidoc does not define`);
	}
	const distinct = new Set(verbs);
	assert.ok(distinct.size >= 4, `the prose cites only ${distinct.size} distinct commands`);
});

test("the prose states the lane contract", () => {
	const { body } = readFrontmatter(text);
	assert.match(body, /lane\/<encounter>\/<minion>/, "the lane box key is missing");
	assert.match(body, /\bDM_PANE\b/, "DM_PANE is missing");
	assert.match(body, /\bDM_SESSION\b/, "DM_SESSION is missing");
	assert.match(body, /lane never commits/i, "the no-commit rule is missing");
	assert.match(body, /wake-up is not a verdict/i, "the wake-up rule is missing");
	assert.match(body, /`blocked`/, "the blocked state is missing");
	assert.match(body, /`done`/, "the done state is missing");
	assert.match(body, /`dm llms`/, "the pointer at dm llms is missing");
	assert.match(body, /do not poll a pane with `dm read`/i, "the no-poll rule is missing");
});
