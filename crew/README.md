# crew

New here? [TRAINING.md](TRAINING.md) walks through crew step by step, with copy-paste blocks.

A local work board for a crew of Claude Code agents. One Go binary keeps the
state (tickets, work items, agents, slots, branch locks, documents, comments,
messages, events) in SQLite, and exposes it through three doors that share
the same core: the CLI (for hooks and scripts), an HTTP/JSON API with an SSE
event stream (for Neovim and other clients), and an MCP server (for Claude).

```sh
go build -o ~/.local/bin/crew ./cmd/crew   # pure Go, no cgo
crew init
```

## Commands

Every command takes `--json`. Errors go to stderr with a non-zero exit.

```
crew init | status
crew ticket add [--jira KEY] [--text TEXT|-] [free text...]   # "read jira ticket VD-21582, focus on X" works
crew ticket show ID|KEY | ls
crew item new --ticket ID [--title T] [--repo R] [--branch B]  # branch defaults to agent/<key|id>
crew item ls [--stage S] | show ID | move ID STAGE [--force]
crew slot add NAME PATH [--clone URL] [--port-offset N] | ls | trust NAME | permissions | lease NAME --agent ID | release NAME
crew agent start ITEM [--slot S] [--role worker] [--model M] [--name N] [--prompt P]
crew agent ls | show ID|NAME | show --session SID | show --branch B [--repo R]
crew agent stop ID | background ID [--continue] [--prompt P] | revive ID [--prompt P]
crew agent rm ID|all [--force] | attach-mark ID on|off
crew msg post (--to AGENT | --item ID) [--kind note|work|review] TEXT|- | ls [--to A] [--status S] | ack ID
crew decision ls [--agent A] [--status open|answered|cancelled|all]   # open ones by default
crew decision show ID | answer ID TEXT|- | answer ID --accept | cancel ID
crew deliver AGENT
crew comment add [--item --document --parent --repo --branch --commit --file --start --end --excerpt-file F|- --text --author] [--send]
crew comment add --stdin-json [--send]     # JSON array (or one object) of comments on stdin
crew comment ls [--item] [--branch] [--repo] [--status] | resolve ID
crew doc add ITEM --kind plan|plan-review|code-review|note FILE|- | ls ITEM | show ID
crew event --hook                          # Claude Code hook handler: reads the payload on stdin, prints nothing
crew event ls [--since N]
crew serve [--addr 127.0.0.1:7767]
crew mcp                                   # MCP server on stdio
```

Stages: new, researching, planned, plan-review, awaiting-you, queued, coding,
pushed, reviewing, pr-draft, human-review, merged, closed, escalated. Moves
follow the lifecycle in the plan (any stage may go to closed or escalated);
`--force` overrides.

## How agents run

`crew agent start` leases a slot, takes the repo+branch lock, checks out the
item's branch and runs `$CREW_CLAUDE --bg -n "<Name>" --mcp-config <file>
--append-system-prompt <instructions> --permission-mode <mode>`
in the slot, with `CREW_AGENT_ID`, `CREW_AGENT_NAME`, `CREW_HOME`,
`CREW_PORT_OFFSET` and `CREW_PROCESS_PREFIX` set. `claude --bg` picks the
session id itself and prints `backgrounded · <short id> · …`; crew reads that
and looks up the full id with `claude agents --json`.

Messages to an idle or stopped agent are delivered right away by
`claude stop <bg>` then `claude --bg --resume <session> "<prompt>"` (or a fresh
start if it never got a prompt). A busy agent gets them on its next `Stop`
hook. An attached agent (open in a client) is never resumed: the client types
the message in and calls `crew msg ack ID`. When the client lets it go,
`crew agent background ID [--continue]` resumes it in the background.

Every start and resume also passes `--mcp-config <CREW_HOME>/agents/<id>/mcp.json`
(rewritten each time) and, for workers, `--append-system-prompt` with
`internal/board/prompts/worker.md`. Neither is part of the conversation, so
both go again on every resume. The config runs the crew binary that started
the agent (its absolute path) as `crew mcp`, with the agent's identity:

```json
{"mcpServers": {"crew": {"command": "/Users/you/.local/bin/crew", "args": ["mcp"],
  "env": {"CREW_HOME": "…", "CREW_AGENT_ID": "3", "CREW_AGENT_NAME": "Fuzzy Muffins"}}}}
```

The instructions tell a worker it's crew agent <Name> on <branch> in this
checkout, not to create worktrees, to put questions on the board with
`ask_decision` (then end its turn), and to call `report_done` after pushing.
Each slot allows `mcp__crew` and `mcp__crew__*`, so the tools never prompt.
An agent taken over in Neovim gets the same tools and instructions (`crew agent
show --json` lists them as `claude_args`).

Agents idle or waiting for input longer than `CREW_STALE_AFTER` (default 2h)
go `stale` (agents `awaiting-decision` never do): crew stops their background session (checked whenever crew runs,
and every minute under `crew serve`). Nothing resumes a stale agent, and
messages to it wait, until `crew agent revive ID`.

Register `crew event --hook` for the `SessionStart`, `UserPromptSubmit`, `Stop`
and `Notification` hooks (`install.sh` does). It ignores sessions that aren't
crew agents, and follows an agent to a new session (fresh start, `/clear`,
opened in Neovim) through `CREW_AGENT_ID` in the hook's environment.

## Decisions

An agent that needs a human calls `ask_decision` once per question (options,
recommendation, reason) and ends its turn. Its `Stop` hook then sets
`awaiting-decision` instead of `idle` (such agents never go stale) and sends
one notification, "crew: <Name> has N decisions". Answers are held until the
agent's last open decision is answered or cancelled; then all answers since
its last batch go to it as one message, delivered like any other (an
`awaiting-decision` agent is resumed straight away):

```
Answers to your decisions:
1. Keep the v1 endpoint?
   → keep it, but log a deprecation warning
2. Which flag name?
   → --fast
```

`crew decision answer` and `cancel` print (`--json`)
`{"decision": {...}, "message": {...}|null}`; `message` is set once the
answers were sent. A decision is `{id, agent_id, agent, item_id, question,
options[], recommendation, reason, status (open|answered|cancelled), answer,
answered_by, message_id, created, answered}`. Agents carry `decisions`, the
number of open ones.

## MCP tools

`crew mcp` (stdio) identifies the caller by `$CREW_AGENT_ID`: `board_status`,
`list_items`, `show_item`, `move_item`, `post_message`, `list_messages`,
`add_comment`, `list_comments`, `report_done {summary}`, `ask_decision
{question, options[], recommendation, reason}` (returns `{id, next}` and tells
the agent to end its turn), `list_decisions {agent?, status?}` (open by
default) and `answer_decision {id, answer | accept}` (for the director).

## HTTP API

GET `/api/status`, `/api/items[?stage=]`, `/api/items/{id}`, `/api/agents`,
`/api/agents/{id|name}`, `/api/agents/by-branch?branch=&repo=`, `/api/slots`,
`/api/messages[?to=&status=]`, `/api/decisions[?agent=&status=]` (open by
default; `status=all`), `/api/decisions/{id}`, `/api/comments[?item=&branch=&repo=&status=]`,
`/api/docs/{item}`, `/api/doc/{id}`, and `/api/events[?since=ID]` (SSE).
POST `/api/items/{id}/move` `{stage, force}`, `/api/messages`
`{to|item_id, kind, from, text}`, `/api/messages/{id}/ack`, `/api/comments`
(an object, an array, or `{comments, send}`), `/api/comments/{id}/resolve`,
`/api/agents/{ref}/attach` `{on}`, `/api/agents/{ref}/deliver`,
`/api/decisions/{id}/answer` `{answer | accept, by}`, `/api/decisions/{id}/cancel`.

## Data and config

- Board: `$CREW_HOME` (default `~/.local/share/crew`): `crew.db`,
  `docs/<item>/<kind>-v<n>.md`, and `agents/<id>/mcp.json`.
- Secrets: `~/.config/crew/env` (or `$CREW_CONFIG`), `KEY=VALUE` lines, never
  in the repo. Real environment variables win.

| Variable | Use |
| --- | --- |
| `CREW_HOME` | board directory |
| `CREW_CLAUDE` | claude binary (default `claude`) |
| `CREW_NO_NOTIFY=1` | no macOS notifications |
| `CREW_STALE_AFTER` | how long an agent may sit idle before it goes stale (default `2h`) |
| `CREW_CONFIG` | config file path |
| `JIRA_BASE_URL`, `JIRA_EMAIL`, `JIRA_API_TOKEN` | fetch tickets via Jira REST v3 |
| `CREW_AGENT_ID` | set for agents; identifies the caller to `crew mcp` and `msg post` |
