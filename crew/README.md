# crew

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
crew slot add NAME PATH [--clone URL] [--port-offset N] | ls | lease NAME --agent ID | release NAME
crew agent start ITEM [--slot S] [--role worker] [--model M] [--name N] [--prompt P]
crew agent ls | show ID|NAME | show --session SID | show --branch B [--repo R]
crew agent stop ID | attach-mark ID on|off
crew msg post (--to AGENT | --item ID) [--kind note|work|review] TEXT|- | ls [--to A] [--status S] | ack ID
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
item's branch and runs
`$CREW_CLAUDE --bg --session-id <uuid> -n "<Name>" --permission-mode <mode>`
in the slot, with `CREW_AGENT_ID`, `CREW_SESSION_ID`, `CREW_HOME`,
`CREW_PORT_OFFSET` and `CREW_PROCESS_PREFIX` set.

Messages to an idle or stopped agent are delivered right away by
`claude stop <bg>` then `claude --bg --resume <session> "<prompt>"`. A busy
agent gets them on its next `Stop` hook. An agent marked attached (a client is
showing its terminal) is never resumed: the client types the message in and
calls `crew msg ack ID`.

Register `crew event --hook` for the `UserPromptSubmit`, `Stop` and
`Notification` hooks. It ignores sessions that aren't crew agents.

## HTTP API

GET `/api/status`, `/api/items[?stage=]`, `/api/items/{id}`, `/api/agents`,
`/api/agents/{id|name}`, `/api/agents/by-branch?branch=&repo=`, `/api/slots`,
`/api/messages[?to=&status=]`, `/api/comments[?item=&branch=&repo=&status=]`,
`/api/docs/{item}`, `/api/doc/{id}`, and `/api/events[?since=ID]` (SSE).
POST `/api/items/{id}/move` `{stage, force}`, `/api/messages`
`{to|item_id, kind, from, text}`, `/api/messages/{id}/ack`, `/api/comments`
(an object, an array, or `{comments, send}`), `/api/comments/{id}/resolve`,
`/api/agents/{ref}/attach` `{on}`, `/api/agents/{ref}/deliver`.

## Data and config

- Board: `$CREW_HOME` (default `~/.local/share/crew`): `crew.db`, and
  `docs/<item>/<kind>-v<n>.md`.
- Secrets: `~/.config/crew/env` (or `$CREW_CONFIG`), `KEY=VALUE` lines, never
  in the repo. Real environment variables win.

| Variable | Use |
| --- | --- |
| `CREW_HOME` | board directory |
| `CREW_CLAUDE` | claude binary (default `claude`) |
| `CREW_NO_NOTIFY=1` | no macOS notifications |
| `CREW_CONFIG` | config file path |
| `JIRA_BASE_URL`, `JIRA_EMAIL`, `JIRA_API_TOKEN` | fetch tickets via Jira REST v3 |
| `CREW_AGENT_ID` | set for agents; identifies the caller to `crew mcp` and `msg post` |
