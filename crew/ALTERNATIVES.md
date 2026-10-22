# Alternatives to crew

What else exists for running a crew of coding agents, how it compares to crew, and what to borrow.
Written October 2026. This field changes monthly, so check each project's own page before relying
on anything here.

**How this was researched.** A web search, mostly comparison blogs and directory pages, several of
them written by vendors promoting their own tool. None of these tools were installed or run for this
review. Read it as a map, not a benchmark.

## Summary

Running several agents in parallel, each in its own git worktree with a dashboard, is common now:
at least five tools do it. If that were all crew did, an existing tool would be the better choice.

No tool found combines what crew is built around:

- **Decisions on a board.** An agent asks questions with options, a recommendation and a reason,
  then ends its turn. You answer, accept or reject from the shell or the editor, and the agent gets
  all the answers in one message.
- **Review comments that reach the code's owner.** A comment on any diff line in Neovim goes to the
  agent working on that branch.
- **Delivery into running sessions.** Messages reach a background agent without restarting it, so
  its connectors stay up.
- **A ticket pipeline with roles.** Worker today; tech lead, interns, reviewer and director planned.
- **Fully local.** One Go binary, SQLite and your existing Claude login. No app to adopt and no
  cloud service.

The closest in spirit is Gas Town. The closest built-in feature is Claude Code's Agent Teams.

## Comparison

| | Parallel agents | Isolation | Roles | Structured decisions | Review routing | Interface | Status |
|---|---|---|---|---|---|---|---|
| **crew** | yes | a slot per agent (a checkout on its own branch) | worker; more planned | yes: batched, accept/reject | diff line comments go to the branch's agent | CLI, HTTP API, MCP, Neovim | in development |
| Conductor | yes | worktree | no | no | diff review in the app | Mac app | active; free tier, Pro $50/month |
| Claude Squad | yes | worktree + tmux | no | no | you review before push | terminal UI | active, open source (Go) |
| Vibe Kanban | yes | worktree | no | no | per task | kanban board | original developer shut it down; community maintained |
| Sculptor | yes | Docker container | no | no | per agent | app | active |
| Crystal | yes | worktree | no | no | per session | desktop app | deprecated Feb 2026 (users pointed to Nimbalyst) |
| Claude Code Agent Teams | yes | in one session | a lead and teammates | no | no | one terminal, split panes | experimental, off by default |
| Gas Town | yes, 20–30 | worktree + git state | yes: Mayor, Polecats, others | no | no | CLI around Claude Code | open source; described as rough and costly |
| withHuman, gotoHuman, Pushary | n/a | n/a | n/a | approval or answer gates for any agent | no | web UI / MCP | active |

## The tools

### Parallel-agent runners

**Conductor** (Melty Labs). A Mac app that runs Claude Code and Codex agents side by side, each in
its own worktree, with a clear diff review and merge flow. Free locally; Pro adds cloud workspaces
and multiplayer. Mac only, with a fixed set of agent types.

**Claude Squad.** An open-source terminal UI in Go. It gives each agent a tmux session and a
worktree; you review its changes before applying or pushing them. Agents don't talk to each other.

**Vibe Kanban.** A kanban board where each card is a task for an agent. Closest to crew's board in
look, but the original developer shut it down and it is now maintained by the community.

**Sculptor.** Runs each agent in its own Docker container instead of a worktree. Heavier to set up,
but a runaway install or a bad command stays inside the container. Worktrees, crew's slots
included, share your machine.

**Crystal.** A desktop app for parallel worktree sessions, deprecated in February 2026.

What they share with crew: isolated checkouts, a list of agents and diff review. What they lack:
roles, a ticket pipeline, structured questions, and comments routed to the agent that owns the code.

### Claude Code Agent Teams

An experimental feature of Claude Code, turned on with `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS`. A
lead session spawns teammates, each a full Claude session, with a shared task list (with
dependencies) that teammates claim work from. Teammates can message each other directly.

It is built in and needs nothing extra. But it all lives inside one terminal session: in-process
teammates can't be resumed, there's no board to drive from the shell or an editor, and nothing
persists between runs. crew's agents are background sessions that outlive the editor and the
terminal, and the board is the record.

### Gas Town

Steve Yegge's open-source orchestrator, released 1 January 2026. It coordinates many Claude Code
sessions through named roles. The **Mayor** is the entry point: a Claude instance with the whole
workspace in view, which you tell your goal. **Polecats** are workers with a lasting identity and
history but short-lived sessions. Several more roles watch, merge and maintain. Work is tracked in
git as **Beads**, grouped into **Convoys** for assignment.

It is the closest to crew's direction: crew's director is a Mayor, and its roles map onto Gas
Town's. It aims much larger (20–30 agents), and by its author's own warning it suits experienced
users. Reports describe it as rough and expensive to run.

### Human-in-the-loop gates

- **withHuman.** Routes agents' approval requests by agent, tool or payload into a queue with an
  audit trail and a web UI. Open-source core, built on an open "Agent Approval Protocol".
- **gotoHuman.** Human reviews and approvals through custom forms, with notifications and an audit
  trail.
- **Pushary.** An `ask_user` MCP tool that takes a question and a type (such as `confirm`), tagged
  with the asking agent, and waits for the answer.
- **human-intervention-mcp.** Structured and branching questions, one browser tab per question. No
  queue.
- **Keldrik's Approval Gate.** An approve, edit or reject card in the chat itself.

These are general approval gates for any agent. None knows about branches, tickets or reviews.
Most block the agent while it waits. crew's agents end their turn instead, so nothing sits running
while you're away, and answers go out together.

## Where crew is different

1. **Questions don't block.** An agent asks and ends its turn. You answer whenever you like; the
   board holds the answers until the last one, then sends them in one message.
2. **Answers mean something.** Accept, reject (with a reason) or cancel are recorded, not just
   typed. Planned: an outcome on every decision, so the board can show how often each agent's
   recommendations are taken.
3. **Your editor is the review tool.** Comment on any line of any diff in Neovim (working tree, a
   commit, the last turn) and it goes to the owner of that branch. On your own branch, to your own
   Claude.
4. **Agents keep running without you.** Quitting Neovim hands the agent back to the background;
   messages reach it there without a restart.
5. **Roles share a checkout.** Planned: a reviewer joins the coder's checkout as a reader, runs the
   code, and sends its findings as comments. One branch, one writer, any number of readers.
6. **No platform.** A single binary, a SQLite file and your Claude login. No app, no account and
   no cloud service.

## What to borrow

- **From Gas Town: lasting agent identity.** Keep an agent's record (decisions accepted or
  rejected, review rounds, items done) across sessions and slots, so the director can weigh it.
- **From Agent Teams: agents messaging each other.** The reviewer and coder pair needs it; today
  crew's messages run from you to an agent.
- **From Sculptor: a stronger sandbox option.** Slots share the machine. A container per slot
  would contain a runaway command, at the cost of setup.
- **From Conductor: the diff review screen.** A single view of each agent's changes, ready to
  merge, would make the "pushed → reviewing" stage faster to clear.
- **From withHuman: an audit trail.** crew's event log already records who answered what and when;
  a readable report on top of it would make that useful.

## When to use something else

- **You just want three agents in parallel on a Mac, with diffs to review:** Conductor.
- **The same in a terminal, open source:** Claude Squad.
- **A one-off fan-out inside a single session:** Claude Code Agent Teams.
- **Dozens of agents, and you accept rough edges:** Gas Town.
- **An approval gate for non-coding agents:** withHuman or gotoHuman.

## Sources

- [9 Open-Source Agent Orchestrators for AI Coding (2026)](https://www.augmentcode.com/tools/open-source-agent-orchestrators)
- [The best AI agent orchestration tools for coding in 2026](https://crystl.dev/blog/best-ai-agent-orchestration-tools/)
- [The Best Tools to Run Multiple Coding Agents in 2026](https://agentsroom.dev/blog/best-multi-agent-coding-tools)
- [Claude Code docs: agent teams](https://code.claude.com/docs/en/agent-teams)
- [Addy Osmani: Claude Code Swarms](https://addyosmani.com/blog/claude-code-agent-teams/)
- [Addy Osmani: The Code Agent Orchestra](https://addyosmani.com/blog/code-agent-orchestra/)
- [Gas Town on GitHub](https://github.com/sebryu/gastown)
- [Steve Yegge releases Gas Town](https://ascii.co.uk/news/article/news-20260102-190a5f9f/steve-yegge-releases-gas-town-multi-agent-orchestrator-for-c)
- [Exploring Gas Town](https://embracingenigmas.substack.com/p/exploring-gas-town)
- [withHuman MCP](https://mcpservers.org/servers/withhuman-ai)
- [gotoHuman MCP Server](https://www.flowhunt.io/mcp-servers/gotohuman)
- [Pushary: human in the loop](https://pushary.com/fr/docs/agents/human-in-the-loop)
