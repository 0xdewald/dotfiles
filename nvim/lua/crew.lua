-- Client for the crew board (crew/, a Go CLI): list agents, open one with its Claude session,
-- route review comments on an agent's branch to that agent. Holds no state of its own: every
-- read and write goes through the `crew` CLI, so another client (an app, a TUI) can replace this.
local M = {}

local function bin()
  if vim.g.crew_bin then return vim.g.crew_bin end
  if vim.fn.executable("crew") == 1 then return "crew" end
  local local_bin = vim.env.HOME .. "/.local/bin/crew"
  return vim.fn.executable(local_bin) == 1 and local_bin or nil
end

function M.available() return bin() ~= nil end

-- Run `crew <args> --json`; returns the decoded result, or nil and the error message
function M.run(args, stdin)
  local b = bin()
  if not b then return nil, "crew isn't installed (run install.sh)" end
  local r = vim.system(vim.list_extend({ b }, vim.list_extend(vim.deepcopy(args), { "--json" })),
    { text = true, stdin = stdin }):wait()
  if r.code ~= 0 then return nil, vim.trim(r.stderr ~= "" and r.stderr or r.stdout) end
  local ok, data = pcall(vim.json.decode, r.stdout ~= "" and r.stdout or "null", { luanil = { object = true, array = true } })
  if not ok then return nil, "crew: unexpected output" end
  return data
end

local function notify(msg, level) vim.notify("Crew: " .. msg, level) end

local function slug(name)
  return (name:lower():gsub("[^%w]+", "-"):gsub("^%-+", ""):gsub("%-+$", ""))
end

-- The same environment crew gives the agent's background session (internal/board agentEnv)
local function agent_env(a)
  return {
    CREW_AGENT_ID = tostring(a.id),
    CREW_AGENT_NAME = a.name,
    CREW_HOME = vim.env.CREW_HOME or (vim.env.HOME .. "/.local/share/crew"),
    CREW_PORT_OFFSET = tostring(a.port_offset or 0),
    CREW_PROCESS_PREFIX = slug(a.name) .. "-",
  }
end

-- Agent attached in this Neovim (its session runs in the Claude split), if any
local function attached() return vim.g.claude_agent end

-- Hand the open agent back to the board, which runs its conversation in the background again
-- (continuing where it left off if it was mid-turn). Space mb does this; so do quitting Neovim,
-- closing the agent's Claude split and opening another agent, so its work never stops with Neovim.
local handing_off = false
local agent_term_buf -- the Claude side panel's terminal for the agent taken over here
function M.background()
  local a = attached()
  if not a then return notify("no crew agent is open here (Space mm to open one)", vim.log.levels.WARN) end
  if a.viewing then
    -- only watching its background session: stop watching, it keeps running there
    handing_off = true
    if require("agent").claude_buf() then require("agent").stop() end
    vim.g.claude_agent = nil
    handing_off = false
    return notify(a.name .. " keeps running in the background")
  end
  local now = M.run({ "agent", "show", tostring(a.id) }) or a
  local mid_turn = now.status == "working" or now.status == "needs-input"
  handing_off = true
  if require("agent").claude_buf() then require("agent").stop() end
  vim.g.claude_agent = nil
  handing_off = false
  local args = { "agent", "background", tostring(a.id) }
  if mid_turn then table.insert(args, "--continue") end
  local res, err = M.run(args)
  if not res then return notify(a.name .. ": " .. err, vim.log.levels.ERROR) end
  notify(("%s continues in the background%s · claude attach %s"):format(a.name,
    mid_turn and " where it left off" or "", res.bg_id))
end

-- Deliver messages the board kept for the attached agent into the Claude split
local poll
local function deliver_pending()
  local a = attached()
  local agent = require("agent")
  if not (a and not a.viewing and agent.claude_buf()) then return end
  local msgs = M.run({ "msg", "ls", "--to", tostring(a.id), "--status", "pending" })
  if not (msgs and #msgs > 0) then return end
  local text = table.concat(vim.tbl_map(function(m) return m.text end, msgs), "\n\n---\n\n")
  if require("claudecode.terminal").send_to_terminal(text) then
    for _, m in ipairs(msgs) do M.run({ "msg", "ack", tostring(m.id) }) end
    notify(("delivered %d message%s to %s"):format(#msgs, #msgs == 1 and "" or "s", a.name))
  end
end

local function same_dir(x, y) return (vim.uv.fs_realpath(x) or x) == (vim.uv.fs_realpath(y) or y) end

-- Go to a tab already in dir; true when there was one
local function goto_tab(dir)
  for i, tab in ipairs(vim.api.nvim_list_tabpages()) do
    if same_dir(vim.fn.getcwd(-1, i), dir) then
      vim.api.nvim_set_current_tabpage(tab)
      return true
    end
  end
  return false
end

-- Open an agent: its folder's tab (or a new one), its conversation resumed in the Claude split.
-- The board marks it attached, so new messages come here instead of resuming it in the background.
function M.open(a)
  if not (a.dir and a.dir ~= "" and vim.fn.isdirectory(a.dir) == 1) then
    return notify(a.name .. " has no folder on this machine", vim.log.levels.WARN)
  end
  a = M.run({ "agent", "show", tostring(a.id) }) or a -- current state, including its conversation
  local agent = require("agent")
  -- already open here: just go to it
  if attached() and attached().id == a.id and agent.claude_buf() then
    goto_tab(a.dir)
    require("claudecode.terminal").ensure_visible({})
    return
  end
  -- Taking over a busy agent interrupts its turn (and cancels a prompt it's waiting on): ask first.
  -- Enter in the dashboard peeks instead, which doesn't.
  if a.status == "working" or a.status == "needs-input" then
    local what = a.status == "working" and "is working" or "is waiting for an answer"
    local choice = vim.fn.confirm(("%s %s. Taking it over interrupts that; it continues here. Take over?"):format(a.name, what),
      "&Take over\n&Peek instead\n&Cancel", 2)
    if choice == 2 then return M.peek(a) end
    if choice ~= 1 then return end
  end
  if attached() then M.background() end -- another agent was open here
  agent.stop()
  -- the session may still be running in the background; stop it there before resuming it here
  if a.bg_id and a.bg_id ~= "" then
    vim.system({ vim.env.CREW_CLAUDE or "claude", "stop", a.bg_id }):wait()
  end
  if not goto_tab(a.dir) then
    vim.cmd("tabnew")
    vim.cmd.tcd(vim.fn.fnameescape(a.dir))
  end
  local ok, err = M.run({ "agent", "attach-mark", tostring(a.id), "on" })
  if not ok and err then return notify(err, vim.log.levels.ERROR) end
  -- Resume the conversation crew says is the agent's. One that never got a prompt has none: start
  -- a fresh session; the board takes it over once you use it (crew follows CREW_AGENT_ID).
  local sid = (a.conversation and a.conversation ~= "") and a.conversation or nil
  agent.start(sid, { track = false, resume = sid ~= nil, env = agent_env(a), agent = a, name = a.name })
  agent_term_buf = nil
  vim.wait(3000, function() return agent.claude_buf() ~= nil end, 20)
  agent_term_buf = agent.claude_buf()
  notify(("%s · %s · %s"):format(a.name, a.branch or "?", vim.fn.fnamemodify(a.dir, ":~")))
end

local function status_hl(s)
  return ({ working = "DiagnosticInfo", idle = "DiagnosticOk", ["needs-input"] = "DiagnosticWarn",
    ["awaiting-decision"] = "DiagnosticError", stopped = "Comment", stale = "Comment", starting = "DiagnosticHint" })[s] or "Normal"
end

-- Peek: a floating panel over the editor to read an agent, check it and leave it running.
-- A running agent shows live (`claude attach`, a plain terminal, separate from the Claude side
-- panel): you can watch it, answer a prompt or type to it, and closing the float leaves it running
-- in the background. One that isn't running (stale, stopped) shows its conversation read-only.
local function title_for(a)
  local n = a.decisions or 0
  local asks = n > 0 and (" · %d decision%s"):format(n, n == 1 and "" or "s") or ""
  return (" %s · %s · %s%s "):format(a.name, a.status or "?", a.branch or "-", asks)
end

local function float_opts(a, footer)
  local size = vim.g.crew_float_size or 0.85
  return {
    position = "float", width = size, height = size, border = "rounded",
    title = title_for(a), title_pos = "center", footer = footer, footer_pos = "center",
  }
end

-- last messages of the agent's conversation, as markdown lines
local function conversation_lines(a)
  local conv = a.conversation ~= "" and a.conversation or a.session_id
  local home = vim.env.CLAUDE_CONFIG_DIR or (vim.env.HOME .. "/.claude")
  local file = ("%s/projects/%s/%s.jsonl"):format(home, ((vim.uv.fs_realpath(a.dir) or a.dir):gsub("[^%w]", "-")), conv or "")
  local ok, raw = pcall(vim.fn.readfile, file)
  if not ok then return { "(no conversation yet)" } end
  local turns = {}
  for _, line in ipairs(raw) do
    local okj, e = pcall(vim.json.decode, line)
    local c = okj and type(e) == "table" and (e.type == "user" or e.type == "assistant") and e.message and e.message.content
    if type(c) == "table" then
      c = table.concat(vim.tbl_map(function(part) return type(part) == "table" and part.type == "text" and part.text or "" end, c), "\n")
    end
    if type(c) == "string" and vim.trim(c) ~= "" and not c:match("^%s*<") then
      table.insert(turns, { who = e.type == "user" and "## You" or "## " .. a.name, text = c })
    end
  end
  local lines = {}
  for i = math.max(1, #turns - 30), #turns do
    vim.list_extend(lines, { turns[i].who, "" })
    vim.list_extend(lines, vim.split(turns[i].text, "\n"))
    table.insert(lines, "")
  end
  return #lines > 0 and lines or { "(no conversation yet)" }
end

local peek_win
function M.peek(a)
  a = M.run({ "agent", "show", tostring(a.id) }) or a
  if peek_win and peek_win:valid() then peek_win:close() end
  local live = a.bg_id and a.bg_id ~= "" and a.status ~= "stale" and a.status ~= "stopped"
  local win
  if live then
    win = Snacks.terminal.open({ vim.env.CREW_CLAUDE or "claude", "attach", a.bg_id }, {
      cwd = a.dir, interactive = true,
      win = float_opts(a, " Esc Esc, then q: close (it keeps running) · Ctrl-t in the dashboard: take over "),
    })
    win:on("BufLeave", function() end) -- keep snacks' default terminal keys
    vim.keymap.set("n", "q", function()
      pcall(vim.fn.jobstop, vim.b[win.buf].terminal_job_id) -- ends the viewer, not the agent
      win:close()
    end, { buffer = win.buf, desc = "Close (agent keeps running)" })
  else
    win = Snacks.win(vim.tbl_extend("force", float_opts(a, " q: close · r: revive · t: take over in the side panel "), {
      text = conversation_lines(a), ft = "markdown", bo = { modifiable = false }, wo = { wrap = true, linebreak = true },
      keys = {
        q = "close",
        r = function(self)
          self:close()
          local r, e = M.run({ "agent", "revive", tostring(a.id) })
          if not r then return notify(e, vim.log.levels.WARN) end
          notify(("revived %s (%s)"):format(r.name, r.status))
          vim.defer_fn(function() M.peek(r) end, 500)
        end,
        t = function(self) self:close(); M.open(a) end,
      },
    }))
    vim.schedule(function() if win:valid() then vim.api.nvim_win_set_cursor(win.win, { vim.api.nvim_buf_line_count(win.buf), 0 }) end end)
  end
  peek_win = win
  -- keep the status in the border current while it's open
  local timer = vim.uv.new_timer()
  timer:start(3000, 3000, vim.schedule_wrap(function()
    if not win:valid() then timer:stop(); timer:close(); return end
    local now = M.run({ "agent", "show", tostring(a.id) })
    if now then pcall(win.set_title, win, title_for(now), "center") end
  end))
end

-- Answer decision d: the agent's recommendation, one of its other options, or your own words.
-- The board holds the answers until the agent's last open decision is answered, then sends them
-- all as one message. done runs after a successful answer.
function M.answer(d, done)
  local choices = {}
  if d.recommendation and d.recommendation ~= "" then
    table.insert(choices, { label = "✓ " .. d.recommendation .. " (recommended)", accept = true })
  end
  for _, o in ipairs(d.options or {}) do
    if o ~= d.recommendation then table.insert(choices, { label = o, answer = o }) end
  end
  table.insert(choices, { label = "Write my own answer…" })
  local function send(args, stdin)
    local r, e = M.run(args, stdin)
    if not r then return notify(e, vim.log.levels.ERROR) end
    if r.message then
      notify(("answers sent to %s (%s)"):format(r.message.to, r.message.status))
    else
      notify(("decision %d answered; %s gets the answers once its other decisions are answered"):format(d.id, d.agent))
    end
    if done then done() end
  end
  local id = tostring(d.id)
  vim.ui.select(choices, { prompt = d.agent .. ": " .. d.question, format_item = function(c) return c.label end }, function(c)
    if not c then return end
    if c.accept then return send({ "decision", "answer", id, "--accept" }) end
    -- answers go on stdin, so one that looks like a flag (--fast) stays an answer
    if c.answer then return send({ "decision", "answer", id, "-" }, c.answer) end
    vim.ui.input({ prompt = "Answer for " .. d.agent .. ": " }, function(text)
      if text and vim.trim(text) ~= "" then send({ "decision", "answer", id, "-" }, text) end
    end)
  end)
end

local function decision_preview(d)
  local lines = { "# " .. d.question, "", ("- from **%s** · item %s · decision %d"):format(d.agent, d.item_id or "-", d.id), "" }
  if #(d.options or {}) > 0 then
    vim.list_extend(lines, { "## Options", "" })
    for _, o in ipairs(d.options) do
      table.insert(lines, "- " .. o .. (o == d.recommendation and "  **(recommended)**" or ""))
    end
    table.insert(lines, "")
  end
  if d.recommendation ~= "" then vim.list_extend(lines, { "## Recommendation", "", d.recommendation, "" }) end
  if d.reason ~= "" then
    vim.list_extend(lines, { "## Why", "" })
    vim.list_extend(lines, vim.split(d.reason, "\n"))
  end
  return lines
end

-- Space md: the open decisions agents put on the board (only agent's, when given). Enter answers
-- one; the list comes back while any are left.
function M.decisions(agent)
  local args = { "decision", "ls" }
  if agent then vim.list_extend(args, { "--agent", tostring(agent.id) }) end
  local ds, err = M.run(args)
  if not ds then return notify(err, vim.log.levels.WARN) end
  if #ds == 0 then return notify(agent and (agent.name .. " has no open decisions") or "no open decisions") end
  Snacks.picker.pick({
    title = agent and ("Decisions · " .. agent.name) or "Crew decisions",
    finder = function()
      return vim.tbl_map(function(d)
        return { decision = d, text = d.agent .. " " .. d.question .. " " .. table.concat(d.options or {}, " ") }
      end, M.run(args) or {})
    end,
    format = function(item)
      local d = item.decision
      return {
        { ("%-20s "):format(d.agent), "Title" },
        { (d.question:gsub("%s+", " ")), "Normal" },
        { d.recommendation ~= "" and ("  → " .. d.recommendation) or "", "Comment" },
      }
    end,
    preview = function(ctx)
      ctx.preview:set_lines(decision_preview(ctx.item.decision))
      ctx.preview:highlight({ ft = "markdown" })
    end,
    confirm = function(p, item)
      p:close()
      if not item then return end
      vim.schedule(function()
        M.answer(item.decision, function()
          local left = M.run(args)
          if left and #left > 0 then M.decisions(agent) end
        end)
      end)
    end,
  })
end

-- Space mm: the agents on the board. Enter peeks at one in a floating panel, Ctrl-t takes it over
-- in the Claude side panel, Ctrl-s sends it a message, Ctrl-d answers its decisions, Ctrl-r
-- revives a stale or stopped one, Ctrl-x stops it.
function M.dashboard()
  local agents, err = M.run({ "agent", "ls" })
  if not agents then return notify(err, vim.log.levels.WARN) end
  local timer
  local picker = Snacks.picker.pick({
    title = "Crew",
    finder = function()
      return vim.tbl_map(function(a)
        return { agent = a, text = table.concat({ a.name, a.status or "", a.branch or "", a.stage or "", a.last_message or "" }, " ") }
      end, M.run({ "agent", "ls" }) or {})
    end,
    format = function(item)
      local a = item.agent
      local mine = attached() and attached().id == a.id
      return {
        { mine and "● " or "  ", "DiagnosticOk" },
        { ("%-20s "):format(a.name), "Title" },
        { ("%-18s "):format(a.status or ""), status_hl(a.status) },
        { ("%-12s "):format(a.stage or "-"), "Comment" },
        { ("%-28s "):format(a.branch or ""), "SnacksPickerGitBranch" },
        { (a.decisions or 0) > 0 and ("? %d "):format(a.decisions) or "", "DiagnosticError" },
        { (a.pending or 0) > 0 and ("✉ %d "):format(a.pending) or "", "DiagnosticWarn" },
        { ((a.last_message or ""):gsub("%s+", " "):sub(1, 80)), "Comment" },
      }
    end,
    preview = function(ctx)
      local a = ctx.item.agent
      local lines = {
        "# " .. a.name, "",
        ("- status: %s · stage: %s · role: %s"):format(a.status or "?", a.stage or "-", a.role or "?"),
        ("- branch: %s · slot: %s (ports +%d)"):format(a.branch or "-", a.slot or "-", a.port_offset or 0),
        "- folder: " .. vim.fn.fnamemodify(a.dir or "", ":~"),
        "- session: " .. (a.session_id or ""),
        (a.decisions or 0) > 0 and ("- **%d open decision%s** (Ctrl-d to answer)"):format(a.decisions, a.decisions == 1 and "" or "s") or "",
        "",
        "## Last message", "",
      }
      vim.list_extend(lines, vim.split(a.last_message ~= "" and a.last_message or "(none yet)", "\n"))
      ctx.preview:set_lines(lines)
      ctx.preview:highlight({ ft = "markdown" })
    end,
    confirm = function(p, item)
      p:close()
      if item then M.peek(item.agent) end
    end,
    actions = {
      crew_message = function(p, item)
        if not item then return end
        vim.ui.input({ prompt = "Message to " .. item.agent.name .. ": " }, function(text)
          if not text or vim.trim(text) == "" then return end
          local m, e = M.run({ "msg", "post", "--to", tostring(item.agent.id), "--from", "you", text })
          if m then notify(("message %s for %s"):format(m.status, item.agent.name)) else notify(e, vim.log.levels.ERROR) end
          p:refresh()
        end)
      end,
      crew_decisions = function(p, item)
        if not item then return end
        p:close()
        vim.schedule(function() M.decisions(item.agent) end)
      end,
      crew_take_over = function(p, item)
        p:close()
        if item then M.open(item.agent) end
      end,
      crew_revive = function(p, item)
        if not item then return end
        local r, e = M.run({ "agent", "revive", tostring(item.agent.id) })
        if r then notify(("revived %s (%s)"):format(r.name, r.status)) else notify(e, vim.log.levels.WARN) end
        p:refresh()
      end,
      crew_stop = function(p, item)
        if not item then return end
        if vim.fn.confirm(("Stop %s and free its slot?"):format(item.agent.name), "&Yes\n&No", 2) ~= 1 then return end
        local _, e = M.run({ "agent", "stop", tostring(item.agent.id) })
        if e then notify(e, vim.log.levels.ERROR) end
        p:refresh()
      end,
    },
    win = { input = { keys = {
      ["<c-s>"] = { "crew_message", mode = { "n", "i" }, desc = "Message agent" },
      ["<c-d>"] = { "crew_decisions", mode = { "n", "i" }, desc = "Answer the agent's decisions" },
      ["<c-x>"] = { "crew_stop", mode = { "n", "i" }, desc = "Stop agent" },
      ["<c-r>"] = { "crew_revive", mode = { "n", "i" }, desc = "Revive a stale or stopped agent" },
      ["<c-t>"] = { "crew_take_over", mode = { "n", "i" }, desc = "Take over in the Claude side panel" },
    } } },
    on_close = function() if timer then timer:stop(); timer:close() end end,
  })
  -- keep statuses current while the dashboard is open
  timer = vim.uv.new_timer()
  timer:start(3000, 3000, vim.schedule_wrap(function()
    if picker and not picker.closed then picker:refresh() end -- keeps the cursor where it is
  end))
end

-- Review comments on a crew agent's branch go to the board, which hands them to that agent
-- (here if it's attached, otherwise by resuming it). Returns true when it took the comments.
function M.route(repo, comments)
  if not M.available() then return false end
  local branch = vim.trim(vim.fn.system({ "git", "-C", repo.root, "branch", "--show-current" }))
  if branch == "" then return false end
  local a = M.run({ "agent", "show", "--branch", branch, "--repo", vim.fn.fnamemodify(repo.root, ":t") })
  if not a then return false end
  local payload = vim.tbl_map(function(c)
    -- commit/side say which version the comment is about ("" = the working tree)
    return { repo = vim.fn.fnamemodify(repo.root, ":t"), branch = branch, commit = c.commit or "", side = c.side or "new",
      file = c.file, start_line = c.start_line, end_line = c.end_line, excerpt = c.excerpt, text = c.text, author = "you" }
  end, comments)
  local res, err = M.run({ "comment", "add", "--stdin-json", "--send" }, vim.json.encode(payload))
  if not res then
    notify(err, vim.log.levels.ERROR)
    return false
  end
  local status = res.message and res.message.status or "pending"
  notify(("sent %d comment%s to %s (%s)"):format(#comments, #comments == 1 and "" or "s", a.name, status))
  vim.schedule(deliver_pending)
  return true
end

function M.setup()
  vim.keymap.set("n", "<leader>mm", M.dashboard, { desc = "Crew dashboard" })
  vim.keymap.set("n", "<leader>md", function() M.decisions() end, { desc = "Crew decisions" })
  vim.keymap.set("n", "<leader>ms", function()
    local s = M.run({ "status" })
    if not s then return notify("not available", vim.log.levels.WARN) end
    vim.notify(vim.inspect(s), vim.log.levels.INFO, { title = "Crew status" })
  end, { desc = "Crew status" })
  local group = vim.api.nvim_create_augroup("crew", { clear = true })
  vim.keymap.set("n", "<leader>mb", M.background, { desc = "Send agent to the background" })
  vim.api.nvim_create_autocmd("VimLeavePre", {
    group = group,
    callback = function() if attached() then M.background() end end,
  })
  -- the open agent's Claude split closed (or Claude exited): it carries on in the background
  vim.api.nvim_create_autocmd({ "TermClose", "BufWipeout" }, {
    group = group,
    callback = function(ev)
      -- only the agent's own Claude panel; other terminals (a peek float, the shell) don't count
      if handing_off or not attached() or ev.buf ~= agent_term_buf then return end
      vim.schedule(function()
        if not handing_off and attached() and not require("agent").claude_buf() then M.background() end
      end)
    end,
  })
  poll = vim.uv.new_timer()
  poll:start(5000, 5000, vim.schedule_wrap(function()
    if attached() then deliver_pending() end
  end))
end

return M
