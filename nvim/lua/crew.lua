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
function M.background()
  local a = attached()
  if not a then return notify("no crew agent is open here (Space mm to open one)", vim.log.levels.WARN) end
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
  if not (a and agent.claude_buf()) then return end
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
  notify(("%s · %s · %s"):format(a.name, a.branch or "?", vim.fn.fnamemodify(a.dir, ":~")))
end

local function status_hl(s)
  return ({ working = "DiagnosticInfo", idle = "DiagnosticOk", ["needs-input"] = "DiagnosticWarn",
    stopped = "Comment", stale = "Comment", starting = "DiagnosticHint" })[s] or "Normal"
end

-- Space mm: the agents on the board. Enter opens one, Ctrl-s sends it a message, Ctrl-r revives a
-- stale or stopped one, Ctrl-x stops it.
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
        { ("%-12s "):format(a.status or ""), status_hl(a.status) },
        { ("%-12s "):format(a.stage or "-"), "Comment" },
        { ("%-28s "):format(a.branch or ""), "SnacksPickerGitBranch" },
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
        "- session: " .. (a.session_id or ""), "",
        "## Last message", "",
      }
      vim.list_extend(lines, vim.split(a.last_message ~= "" and a.last_message or "(none yet)", "\n"))
      ctx.preview:set_lines(lines)
      ctx.preview:highlight({ ft = "markdown" })
    end,
    confirm = function(p, item)
      p:close()
      if item then M.open(item.agent) end
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
      ["<c-x>"] = { "crew_stop", mode = { "n", "i" }, desc = "Stop agent" },
      ["<c-r>"] = { "crew_revive", mode = { "n", "i" }, desc = "Revive a stale or stopped agent" },
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
  local commit = vim.trim(vim.fn.system({ "git", "-C", repo.root, "rev-parse", "HEAD" }))
  local payload = vim.tbl_map(function(c)
    return { repo = vim.fn.fnamemodify(repo.root, ":t"), branch = branch, commit = commit, file = c.file,
      start_line = c.start_line, end_line = c.end_line, excerpt = c.excerpt, text = c.text, author = "you" }
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
      if handing_off or not (attached() and vim.bo[ev.buf].buftype == "terminal") then return end
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
