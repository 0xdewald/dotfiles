# Crew training: learn it by doing

A hands-on walkthrough of crew, the local board for running several Claude agents side by side. Every step is a block you can copy and paste. Change the values at the top of a block (marked `# ← change`) and leave the rest.

It takes about 20 minutes. By the end you'll have:
- two agents, each in its own checkout and branch,
- sent one of them a message,
- answered the questions an agent put on the board,
- opened it in Neovim, reviewed its changes and sent it review comments,
- moved its work through the stages, and cleaned up.

For the reference of every command, see [README.md](README.md). For the Neovim side in general, see [../GETTING_STARTED.md](../GETTING_STARTED.md).

---

## 0. The words you'll see

| Word | What it is |
|---|---|
| **Board** | crew's memory: a small database at `~/.local/share/crew/crew.db`. Everything below is stored there. |
| **Ticket** | What needs doing: a Jira key, pasted text, or both. |
| **Work item** | One piece of work for one ticket, with a branch and a **stage** (new → queued → coding → pushed → …). |
| **Slot** | A separate checkout of a repo that one agent works in. It's a normal `git clone`, not your own working copy, so the agent can switch branches without touching your work. |
| **Agent** | A Claude session crew started for a work item, with a generated name like "Sneaky Sprockets". It runs in the background in its slot. |
| **Message** | A note for an agent. It waits ("pending") until the agent is free, then gets delivered. |
| **Decision** | A question an agent put on the board for you, with the options it sees, the one it recommends and why. |
| **Comment** | A review comment on a line of the agent's code. It's stored on the board and sent to the agent. |

---

## 1. Install (once)

```sh
~/dotfiles/install.sh
```

When it shows the change to `~/.claude/settings.json` (Claude Code hooks), answer `y`. The hooks are how the board learns that an agent started working, finished, or needs input.

Already installed and only need crew and its hooks? Run these instead:

```sh
cd ~/dotfiles/crew && go build -o ~/.local/bin/crew ./cmd/crew && cd -
crew init
~/dotfiles/install.sh hooks
```

**Check it worked:**

```sh
crew --help | head -3
crew status
jq '.hooks | keys' ~/.claude/settings.json
```

You should see the crew usage text, an empty agent table, and a hook list that includes `Stop` and `Notification`.

---

## 2. Optional: connect Jira

Without this, crew stores just the ticket key (`PROJ-123`), and you paste the details yourself. With it, crew fetches the ticket's title and description.

```sh
JIRA_BASE_URL="https://your-company.atlassian.net"   # ← change
JIRA_EMAIL="you@your-company.com"                    # ← change
JIRA_API_TOKEN="paste-your-token-here"               # ← change (https://id.atlassian.com/manage-profile/security/api-tokens)

mkdir -p ~/.config/crew
cat > ~/.config/crew/env <<EOF
JIRA_BASE_URL=$JIRA_BASE_URL
JIRA_EMAIL=$JIRA_EMAIL
JIRA_API_TOKEN=$JIRA_API_TOKEN
EOF
chmod 600 ~/.config/crew/env
```

This file lives outside the dotfiles repo, so the token is never committed.

---

## 3. Make a slot (an agent's own checkout)

A slot is a fresh clone of the repo the agent will work in. Make one per agent you want running at the same time.

```sh
REPO=my-service                                   # ← change: the repo's folder name
REPO_URL=$(git -C ~/code/$REPO remote get-url origin)   # ← change ~/code to where your repo lives
SLOT=slot-one                                     # ← change if you like

crew slot add "$SLOT" ~/agents/$SLOT/$REPO --clone "$REPO_URL"
```

Expected output:

```
slot slot-one /Users/you/agents/slot-one/my-service (port offset 0)
next: allow Claude to work there once with `crew slot trust slot-one`
```

crew also writes the slot's **Claude permissions** into `.claude/settings.local.json` in its checkout (kept out of git through the repo's local exclude list):
- its agent may read and edit anything in its own slot without asking, and is denied every other slot;
- it works in the slot's checkout directly, on its `agent/…` branch, instead of moving into a git worktree (Claude's default for background sessions);
- it may commit and push `agent/…` branches, and may never push to `main`, `master` or `develop` or force-push. Deny rules hold in every permission mode.

Slots added before crew did this: run `crew slot permissions` once.

**Allow Claude to work in the slot (once per slot).** Claude Code only starts background sessions in folders you've trusted. This opens Claude in the slot so you can accept its prompt:

```sh
crew slot trust "$SLOT"
```

When Claude asks whether to trust the folder, accept, then type `/exit`. crew never accepts this for you.

Make a second slot so two agents can run side by side:

```sh
SLOT=slot-two                                     # ← change if you like
crew slot add "$SLOT" ~/agents/$SLOT/$REPO --clone "$REPO_URL"
crew slot trust "$SLOT"
crew slot ls
```

```
NAME      PORTS  AGENT  PATH
slot-one  +0     -      /Users/you/agents/slot-one/my-service
slot-two  +100   -      /Users/you/agents/slot-two/my-service
```

**What "PORTS +100" means:** each slot gets a port offset, which the agent sees as `$CREW_PORT_OFFSET`. If your app normally runs on port 3000, the agent in slot-two should use 3100, so two agents' dev servers don't clash. Agents also get `$CREW_PROCESS_PREFIX` (like `sneaky-sprockets-`) for naming `pm2` processes or containers.

Already have a clone you want to use? Leave out `--clone`:

```sh
crew slot add slot-three ~/agents/slot-three/my-service   # ← change
```

---

## 4. Add a ticket

**From Jira** (works with or without step 2). crew finds the key anywhere in the text, so you can write naturally:

```sh
TICKET="read jira ticket PROJ-123, keep the API backwards compatible"   # ← change
crew ticket add "$TICKET"
```

```
ticket 1 PROJ-123
```

**From pasted text.** Good for things that aren't in Jira:

```sh
crew ticket add --text - <<'EOF'
Users get logged out after 5 minutes. Make the session last 8 hours.
Keep the refresh token flow as it is.
EOF
```

**Both together:** put the key in the text and add your own notes after it.

```sh
crew ticket ls
crew ticket show PROJ-123          # ← change: a key or a ticket number
```

---

## 5. Create work items

A work item ties a ticket to a branch. By default the branch is `agent/<jira key>`, or `agent/<item number>` when there's no key.

```sh
TICKET_ID=1                        # ← change: the number from `crew ticket ls`
crew item new --ticket $TICKET_ID --title "Keep API compatible"
```

```
item 1 Keep API compatible on agent/PROJ-123
```

Pick your own branch name with `--branch`:

```sh
crew item new --ticket 2 --branch agent/session-timeout    # ← change
crew item ls
```

```
ID  STAGE  KEY       BRANCH                 AGENT  TITLE
1   new    PROJ-123  agent/PROJ-123         -      Keep API compatible
2   new    -         agent/session-timeout  -      Users get logged out after 5 minutes…
```

**Stages:** new items start at `new`. Move them to `queued` when they're ready for an agent:

```sh
ITEM=1                             # ← change
crew item move $ITEM queued
```

crew only allows sensible moves and tells you the options if you get it wrong:

```
crew: item 1: can't move queued → merged (allowed: coding, closed, escalated; use --force)
```

The full path is: `new → researching → planned → plan-review → awaiting-you → queued → coding → pushed → reviewing → pr-draft → human-review → merged`. Any stage can go to `closed` or `escalated`. In this first version, the stages before `queued` are for later milestones: you go straight from `new` to `queued`.

---

## 6. Start an agent

```sh
ITEM=1                                                        # ← change
PROMPT="Read the ticket and propose a plan before coding."   # ← change: the agent's first instruction
crew agent start $ITEM --prompt "$PROMPT"
```

```
started Sneaky Sprockets in /Users/you/agents/slot-one/my-service on agent/PROJ-123
```

What just happened:
1. crew picked a free slot (or use `--slot slot-two` to choose one),
2. created and checked out `agent/PROJ-123` in that slot, locked so no other slot can take the same branch,
3. started a background Claude there, named "Sneaky Sprockets", with your prompt as its first message. Workers run in **auto mode**: routine actions (reading the ticket, editing, running tests, committing) go ahead without asking, and anything risky still asks. When an agent asks, it shows as `needs-input` and you get a notification; open it (step 9) to answer. To use a different mode, add e.g. `CREW_WORKER_PERMISSION_MODE=acceptEdits` to `~/.config/crew/env`.
4. moved the item to `coding`.

Running `crew agent start` again on an item that already has an agent doesn't start a second one: it gives that agent the ticket again (now if it's free, when its turn ends if it's busy, or by reviving it if it's stale). Add `--prompt "…"` to say something extra with it.

Useful options:

```sh
crew agent start 2 --slot slot-two                 # choose the slot
crew agent start 2 --model opus                    # choose the model
crew agent start 2 --name "Night Owl"              # choose the name
```

**Start the second agent** now, so you have two running:

```sh
crew item move 2 queued
crew agent start 2 --slot slot-two --prompt "Find where the session length is set and explain it first."   # ← change
```

---

## 7. Watch your agents

**From the shell:**

```sh
crew agent ls
```

```
ID  NAME              STATUS    STAGE   BRANCH          SLOT      MSGS  DECISIONS  LAST
1   Sneaky Sprockets  working   coding  agent/PROJ-123  slot-one  0     0          -
2   Lazy Muffins      idle      coding  agent/session…  slot-two  0     0          I found it in config/session.ts…
```

- **STATUS:** `starting` (just launched), `working` (busy), `idle` (finished its turn, waiting for you), `needs-input` (asking a question), `awaiting-decision` (it put questions on the board, see step 8½), `stale` (left idle too long, see below), `stopped`.
- **DECISIONS:** how many of its questions are waiting for your answer.

**Stale agents.** An agent that sits idle (or waiting for input) for more than 2 hours goes **stale**: crew stops its background process so it isn't left running for nothing. It keeps its slot, branch and conversation, and doesn't run again, even when you message it, until you revive it:

```sh
AGENT="Sneaky Sprockets"           # ← change
crew agent revive "$AGENT"                                   # back in the background, with any waiting messages
crew agent revive "$AGENT" --prompt "Carry on with the tests."   # ← change: or with a new instruction
```

Opening it in Neovim (step 9) revives it too. To change the limit, add `CREW_STALE_AFTER=4h` (or `90m`, …) to `~/.config/crew/env`.
- **LAST:** the agent's last message, recorded when it finishes a turn. You also get a macOS notification: "crew: Lazy Muffins is done".

Details for one agent:

```sh
AGENT="Sneaky Sprockets"           # ← change: name or number
crew agent show "$AGENT"
```

See what an agent is doing right now, using Claude Code's own background-session commands. The short id is the `bg` value from `crew agent show`:

```sh
BG=dcd3a980                        # ← change: the "bg" id from `crew agent show`
claude logs $BG                    # recent output
claude attach $BG                  # jump into the session in this terminal (Ctrl-Z to leave it running)
claude agents                      # Claude Code's list of all sessions
```

**From Neovim:** press `Space m m` for the crew dashboard. It shows the same list, refreshes every few seconds, and has a preview with the last message on the right.

---

## 8. Send an agent a message

```sh
AGENT="Sneaky Sprockets"                    # ← change
MESSAGE="Also add a test for the expiry."   # ← change
crew msg post --to "$AGENT" "$MESSAGE"
```

```
message 1 to Sneaky Sprockets: pending
```

**How it gets there.** If the agent's session is running in the background, crew hands the message to it through a **mailman**: a short headless Claude (a small model, one tool call, a few seconds) that uses Claude's session messaging to deliver it. The agent's session keeps running, so its connectors (Jira, Docs, Slack, …) stay connected. If the session isn't running, or the mailman can't deliver, crew resumes the session with the message instead. `crew event ls` shows which way each message went (`"via":"mailman"` or `"via":"resume"`).

**"pending" is normal.** A message waits until the agent is free:
- If the agent is **idle or stopped**, crew delivers it straight away by resuming the session with your message.
- If it's **busy**, the message waits and is delivered the moment the agent finishes its turn.
- If the agent is **open in your Neovim**, the message is typed into its Claude split instead.
- If the agent is **stale**, the message waits until you revive it, and crew tells you so.

Check delivery:

```sh
crew msg ls --to "$AGENT"
```

Several messages waiting for the same agent are delivered together as one.

From Neovim: in the dashboard (`Space m m`), select the agent and press `Ctrl-s`.

---

## 8½. Answer an agent's decisions

Agents don't ask you things in chat. When an agent needs a decision, it puts each question on the board (with the options it sees, the one it recommends and why) and ends its turn. Its status becomes `awaiting-decision` and you get one notification: "crew: Sneaky Sprockets has 2 decisions". It doesn't go stale while it waits.

It knows to do this because crew gives every worker its tools (the crew MCP server) and short instructions each time it starts or resumes. Nothing to set up.

**See what's waiting:**

```sh
crew decision ls
```

```
ID  STATUS  AGENT             ITEM  QUESTION                RECOMMENDED  ANSWER
3   open    Sneaky Sprockets  1     Keep the v1 endpoint?   keep it      -
4   open    Sneaky Sprockets  1     Which flag name?        --fast       -
```

**Read one in full** (options, recommendation, why):

```sh
DECISION=3                         # ← change
crew decision show $DECISION
```

**Answer.** Take the agent's recommendation, or write your own answer:

```sh
crew decision answer 3 --accept                                   # ← change: use its recommendation
crew decision answer 4 "Use --quick, it matches the old script"   # ← change: your own answer
```

The agent hears nothing until **all** its open decisions are answered. Then it gets every answer together as one message and carries on:

```
Answers to your decisions:
1. Keep the v1 endpoint?
   → keep it
2. Which flag name?
   → Use --quick, it matches the old script
```

A question that no longer matters: `crew decision cancel 4`. Other filters: `crew decision ls --agent "Sneaky Sprockets"`, `crew decision ls --status all`.

**From Neovim:** press `Space m d`. It lists the open decisions of every agent, with the options, the recommendation and the reason on the right. Press Enter on one and choose:
- `✓ keep it (recommended)`, the agent's recommendation,
- one of its other options,
- `Write my own answer…` to type one.

In the dashboard (`Space m m`), agents with open decisions show `? 2`, and `Ctrl-d` on an agent answers just that agent's decisions.

---

## 9. Open an agent in Neovim

```sh
nvim
```

Press `Space m m` and move to the agent. You have two ways to open it:

**Enter: peek.** A floating panel over the editor, with the agent's name, status and branch in its border.
- A running agent shows **live**: watch it, answer a permission prompt, or type to it. Don't press `Esc` in the panel: it goes to Claude and interrupts the agent's turn. Close the panel with `Ctrl-q` (or `Ctrl-\` `Ctrl-n`, then `q`) and it carries on in the background. Nothing is stopped or moved, and your own Claude side panel isn't touched.
- A stale or stopped agent shows its **conversation read-only**: `r` revives it, `t` takes it over, `q` closes.

**Ctrl-t: take over.** The agent moves into the Claude side panel, connected to Neovim: it sees your file and selection, and its edits show as diffs. Use this to work with it closely. If it's in the middle of a turn, Neovim asks first, because taking over interrupts that turn. When you take it over, Neovim:
1. stops the agent's background session (its conversation is kept),
2. opens its folder's tab (or a new one),
3. resumes the same conversation in the Claude split on the right,
4. tells the board the agent is "attached", so new messages and comments go into this split.

You can now type to the agent directly, as you would with any Claude in Neovim.

**Sending it back to the background.** Press `Space m b`. Neovim stops its Claude split and crew resumes the same conversation in the background. If the agent was in the middle of a turn, crew tells it to continue where it left off, so its work carries on. You get the id to watch it with: `claude attach <id>`.

You don't have to remember this: **quitting Neovim, closing the agent's Claude split, or opening a different agent does the same**, so an agent's work never stops because Neovim closed. (A turn that was running is interrupted for a moment and picks up again in the background.)

---

## 10. Review its work and send comments

With the agent open (step 9), in its tab:

| Keys | What it does |
|---|---|
| `Space r v` | Diff only what the agent changed in its last turn |
| `Space r V` | Diff everything that's not committed yet |
| `Space r N` | Diff a range of commits: type `3` for the last three, or a branch like `main` for everything since it |
| `Space r c` | Comment on the current line (or a selected range in visual mode) |
| `Space r s` | Send your comments |

Because this branch belongs to a crew agent, `Space r s` sends the comments **through the board** to that agent. They're stored on the board, and the agent gets one message listing each file and line, the code, and your comment.

Check the stored comments from the shell:

```sh
BRANCH=agent/PROJ-123              # ← change
crew comment ls --branch $BRANCH
```

Mark one resolved once the agent has fixed it:

```sh
crew comment resolve 1             # ← change: the comment number
```

---

## 11. Move the work along

In this first version, you move the stages yourself as the work progresses:

```sh
ITEM=1                             # ← change
crew item move $ITEM pushed        # the agent pushed its branch
crew item move $ITEM reviewing     # you (or later a reviewer agent) are reviewing it
crew item move $ITEM coding        # changes requested: back to the agent (counts a review round)
crew item move $ITEM pr-draft      # approved; a draft PR exists
crew item show $ITEM
```

Later milestones automate these moves: agents report "done" themselves, a reviewer agent reviews, and draft PRs open automatically.

---

## 12. Stop agents and clean up

Stop an agent. Its conversation is kept, and its slot and branch lock are freed for the next agent:

```sh
AGENT="Sneaky Sprockets"           # ← change
crew agent stop "$AGENT"
crew slot ls                       # its slot shows "-" again
```

Remove agents you're done with from the board. Their Claude conversations aren't deleted, and their work item goes back to `queued`:

```sh
crew agent rm "$AGENT"             # a stopped agent
crew agent rm "$AGENT" --force     # a running one: stops it first
crew agent rm all                  # every stopped agent
```

Close a work item you don't need:

```sh
crew item move 2 closed --force    # ← change: the item number
```

The slot folders stay on disk, so the next agent can reuse them. They're ordinary clones. To remove one for good, delete the folder yourself:

```sh
rm -rf ~/agents/slot-two           # ← change; only once nothing runs there
```

---

## 13. When something goes wrong

**See what happened, newest last:**

```sh
crew event ls | tail -20
```

**Common messages:**

| You see | What to do |
|---|---|
| `… doesn't exist: clone a repo there first …` | The slot folder isn't there. Use `crew slot add … --clone <url>`, or clone it yourself first. |
| `Claude hasn't been allowed to work in … yet` | Run `crew slot trust <slot>`, accept the prompt, type `/exit`, then start the agent again. Nothing was left behind by the failed start. |
| `branch agent/X is locked by slot Y` | Another agent has that branch. Stop it (`crew agent stop …`) or use another branch. |
| `no free slot` | All slots are busy. Stop an agent, or add a slot (step 3). |
| An agent stays `starting` | It hasn't finished its first turn yet, or the hooks aren't registered. Check with `jq '.hooks | keys' ~/.claude/settings.json`, and run `~/dotfiles/install.sh hooks` if `Stop` is missing. |
| An agent stays `awaiting-decision` | It's waiting on your answers: `crew decision ls` or `Space m d`. |
| A message stays `pending` | The agent is still busy, or open in Neovim: it's delivered when the agent finishes its turn. If the agent is `stale`, revive it: `crew agent revive <name>`. |
| `warning: JIRA_… not set` | Only the key was stored. Connect Jira (step 2) or paste the details as text. |

**Where things are:**

| What | Where |
|---|---|
| The board | `~/.local/share/crew/crew.db` (change with `CREW_HOME`) |
| Documents (plans, reviews) | `~/.local/share/crew/docs/` |
| Jira settings | `~/.config/crew/env` |
| Agents' checkouts | wherever you created the slots, e.g. `~/agents/` |

**Start over with an empty board.** This deletes all crew history: tickets, items, messages and comments. Agents' conversations and slot folders aren't touched.

```sh
crew agent ls                      # stop anything still running first: crew agent stop <name>
mv ~/.local/share/crew ~/.local/share/crew.bak-$(date +%Y%m%d)
crew init
```

---

## Optional: the background server

`crew serve` checks every minute for agents that have gone stale (otherwise that happens whenever you run a crew command) and serves the local web API for future apps. Neovim doesn't need it.

```sh
crew serve start      # runs in the background; logs to ~/.local/share/crew/serve.log
crew status           # first line: running (green), not running (red), or "crew serve restart" after a rebuild (yellow)
crew serve restart    # after rebuilding crew
crew serve logs       # last lines of its log
crew serve stop
```

## 14. Cheat sheet

```sh
# setup
crew slot add NAME ~/agents/NAME/REPO --clone URL  &&  crew slot trust NAME
crew ticket add "read jira ticket PROJ-123 …"   |   crew ticket add --text -
crew item new --ticket N [--branch agent/x]  &&  crew item move N queued

# agents
crew agent start ITEM [--slot S] [--prompt "…"] [--model M]
crew agent ls  |  crew agent show NAME  |  crew agent stop NAME
crew agent revive NAME [--prompt "…"]  |  crew agent rm NAME|all [--force]
crew msg post --to NAME "…"  |  crew msg ls --to NAME
crew decision ls  |  crew decision answer N --accept  |  crew decision answer N "…"

# review and progress
crew comment ls --branch agent/x  |  crew comment resolve N
crew item move N pushed|reviewing|coding|pr-draft|closed
crew event ls | tail

# Neovim
Space m m   dashboard (Enter peek · Ctrl-t take over · Ctrl-s message · Ctrl-d decisions · Ctrl-r revive · Ctrl-x stop)
Space m d   answer agents' decisions (Enter: recommendation, another option, or your own)
Space m b   send the open agent back to the background (quitting Neovim does this too); closes a peek
Space r v / r V / r N   diff last turn / uncommitted / commit range
Space r c / r s         comment / send
```
