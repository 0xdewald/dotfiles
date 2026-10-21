-- Claude in Neovim (claudecode.nvim's terminal):
-- - keeps the Claude window in place next to the editor
-- - gives each folder or workspace its own Claude session: started with Neovim, resumed next time
local M = {}

local function claude_buf()
  local ok, term = pcall(require, "claudecode.terminal")
  local buf = ok and term.get_active_terminal_bufnr() or nil
  return buf and vim.api.nvim_buf_is_valid(buf) and buf or nil
end
M.claude_buf = claude_buf

local function claude_win()
  local buf = claude_buf()
  local win = buf and vim.fn.bufwinid(buf) or -1
  return win ~= -1 and win or nil
end

-- A window to open files in: a normal split that isn't Claude's, a sidebar or a terminal
local function editor_win(except)
  for _, win in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
    local buf = vim.api.nvim_win_get_buf(win)
    if win ~= except and vim.api.nvim_win_get_config(win).relative == "" and not vim.w[win].claude_pinned
      and not vim.w[win].snacks_layout and vim.bo[buf].buftype ~= "terminal" then
      return win
    end
  end
end

-- A file landed in Claude's window (e.g. the explorer had no editor window to use):
-- put Claude back and open the file in an editor window beside it instead. This must run
-- before snacks' own fixbuf handler, which in that case opens the file and closes Claude's window.
local function unpin(win, buf)
  local cb = claude_buf()
  if not (cb and vim.api.nvim_win_is_valid(win) and vim.api.nvim_win_get_buf(win) == buf) then return end
  local cursor = vim.api.nvim_win_get_cursor(win)
  vim.api.nvim_win_set_buf(win, cb)
  local target = editor_win(win)
  if target then
    vim.api.nvim_win_set_buf(target, buf)
  else
    target = vim.api.nvim_open_win(buf, false, { split = "left", win = win })
    -- The sidebar grew while Claude was its only neighbour; give that space to the new editor
    for _, w in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
      if vim.w[w].snacks_layout and vim.api.nvim_win_get_config(w).relative == "" then
        pcall(vim.api.nvim_win_set_width, w, 40)
      end
    end
  end
  vim.api.nvim_set_current_win(target)
  pcall(vim.api.nvim_win_set_cursor, target, cursor)
  -- the explorer focuses the window it opened the file in (Claude's) afterwards; take focus back
  vim.schedule(function()
    if vim.api.nvim_win_is_valid(target) then vim.api.nvim_set_current_win(target) end
  end)
end

-- Sessions -------------------------------------------------------------------------------------
-- The session ID for a folder/workspace lives in <state>/agents/<key>.id. Claude is started with
-- NVIM_CLAUDE_SESSION_FILE pointing at that file, and the SessionStart hook (bin/claude-session)
-- rewrites it whenever Claude's session changes, e.g. after /clear.
local state_dir = vim.fn.stdpath("state") .. "/agents"

local function name() return vim.g.workspace_name or vim.fn.fnamemodify(vim.fn.getcwd(), ":t") end

local function id_file()
  local key = vim.g.workspace_name and "workspace-" .. vim.g.workspace_name or vim.fn.getcwd():gsub("[^%w]", "-")
  return state_dir .. "/" .. key .. ".id"
end

local function current_id()
  local ok, lines = pcall(vim.fn.readfile, id_file())
  return ok and lines[1] and lines[1] ~= "" and lines[1] or nil
end

-- Claude keeps each conversation in ~/.claude/projects/<cwd, non-alphanumerics as "-">/<id>.jsonl
local function project_dir(cwd)
  local home = vim.env.CLAUDE_CONFIG_DIR or vim.fn.expand("~/.claude")
  return home .. "/projects/" .. (vim.uv.fs_realpath(cwd) or cwd):gsub("[^%w]", "-")
end

-- Folder of the running Claude; before one is started, the folder it will start in
local function claude_cwd() return claude_buf() and vim.g.claude_cwd or vim.fn.getcwd() end

local function session_exists(id) return vim.uv.fs_stat(project_dir(claude_cwd()) .. "/" .. id .. ".jsonl") ~= nil end

-- Environment for the next Claude start (read by claudecode when it starts Claude): point the
-- SessionStart hook at this folder's id file, unless opts.track is false (e.g. a crew agent's
-- session, which mustn't replace the folder's own). opts.env adds variables for this start only.
local extra_env = {}
local function prepare(opts)
  opts = opts or {}
  local env = require("claudecode").state.config.env
  for k in pairs(extra_env) do env[k] = nil end
  extra_env = opts.env or {}
  for k, v in pairs(extra_env) do env[k] = v end
  if opts.track == false then
    env.NVIM_CLAUDE_SESSION_FILE = nil
  else
    vim.fn.mkdir(state_dir, "p")
    env.NVIM_CLAUDE_SESSION_FILE = id_file()
  end
  vim.g.claude_agent = opts.agent -- set for crew agents (lua/crew.lua), nil otherwise
end

-- Open Claude on a session: resume it when it exists, otherwise start a new one with that ID.
-- opts (all optional): track = false to leave the folder's saved session alone, env, agent, name,
-- resume = true when the caller knows id is an existing conversation (crew does).
function M.start(id, opts)
  opts = opts or {}
  prepare(opts)
  local args
  if id and (opts.resume or session_exists(id)) then
    args = "--resume " .. id
  else
    id = id or vim.trim(vim.fn.system("uuidgen")):lower()
    args = ("--session-id %s -n %s"):format(id, vim.fn.shellescape(opts.name or name()))
  end
  if opts.track ~= false then vim.fn.writefile({ id }, id_file()) end
  require("claudecode.terminal").open({}, args)
end

-- Show a background session live in the Claude split (`claude attach`): it keeps running there,
-- and closing this view leaves it running. opts as for M.start.
function M.view(bg_id, opts)
  prepare(opts or {})
  require("claudecode.terminal").open({}, "attach " .. bg_id)
end

function M.stop()
  local buf = claude_buf()
  if not buf then return end
  local job = vim.b[buf].terminal_job_id
  pcall(vim.fn.jobstop, job)
  -- wait for the process to exit, so its session is free: resuming it elsewhere (crew hands an
  -- agent back to the background) otherwise gets a copy of the conversation instead
  if job then pcall(vim.fn.jobwait, { job }, 5000) end
  require("claudecode.terminal").close()
  vim.wait(3000, function() return claude_buf() == nil end, 20)
  if claude_buf() then pcall(vim.api.nvim_buf_delete, buf, { force = true }) end
end

-- Space a c: show/hide Claude, starting (or resuming) this folder's session when it isn't running
function M.toggle()
  if claude_buf() then return require("claudecode.terminal").simple_toggle({}) end
  M.start(current_id())
end

-- Space a n: drop the current conversation and start a fresh one (the old one stays in Space a l)
function M.new()
  M.stop()
  M.start(nil)
end

-- Space a r / a C: Claude's own resume picker / most recent conversation, still tracked by the hook
function M.claude_args(args)
  M.stop()
  prepare()
  require("claudecode.terminal").open({}, args)
end

-- Sessions Claude has for the folder it runs in, newest first
function M.sessions()
  local dir, list = project_dir(claude_cwd()), {}
  for file, kind in vim.fs.dir(dir) do
    local id = kind == "file" and file:match("^(.+)%.jsonl$")
    if id then
      local path = dir .. "/" .. file
      local title, prompt
      for line in io.lines(path) do
        local t = line:match('"customTitle":"(.-)"') or line:match('"aiTitle":"(.-)"')
        if t then title = t end
        if not prompt and line:find('"type":"user"', 1, true) then
          local ok, e = pcall(vim.json.decode, line)
          local c = ok and e.message and e.message.content
          if type(c) == "string" and not c:match("^<") then prompt = c end
        end
      end
      table.insert(list, { id = id, path = path, mtime = vim.uv.fs_stat(path).mtime.sec,
        title = title or (prompt and prompt:gsub("%s+", " "):sub(1, 80)) or "(empty)" })
    end
  end
  table.sort(list, function(a, b) return a.mtime > b.mtime end)
  return list
end

-- Space a l: this folder's Claude sessions. Enter switches to one, Ctrl-n starts a new one,
-- Ctrl-x deletes one (asks first).
function M.list()
  local running = claude_buf() and current_id()
  Snacks.picker.pick({
    title = "Claude sessions · " .. name(),
    finder = function()
      return vim.tbl_map(function(sess)
        return { session = sess, text = sess.title .. " " .. sess.id }
      end, M.sessions())
    end,
    format = function(item)
      local sess = item.session
      return {
        { sess.id == running and "● " or "  ", "DiagnosticOk" },
        { os.date("%b %d %H:%M  ", sess.mtime), "Comment" },
        { sess.title },
      }
    end,
    preview = function(ctx)
      local lines = {}
      for line in io.lines(ctx.item.session.path) do
        local ok, e = pcall(vim.json.decode, line)
        local c = ok and (e.type == "user" or e.type == "assistant") and e.message and e.message.content
        if type(c) == "table" then
          c = table.concat(vim.tbl_map(function(p) return p.type == "text" and p.text or "" end, c), "\n")
        end
        if type(c) == "string" and vim.trim(c) ~= "" and not c:match("^<") then
          table.insert(lines, (e.type == "user" and "## You" or "## Claude"))
          vim.list_extend(lines, vim.split(c, "\n"))
          table.insert(lines, "")
        end
      end
      ctx.preview:set_lines(lines)
      ctx.preview:highlight({ ft = "markdown" })
    end,
    confirm = function(picker, item)
      picker:close()
      if not item or item.session.id == running then return M.toggle() end
      M.stop()
      M.start(item.session.id)
    end,
    actions = {
      agent_new = function(picker)
        picker:close()
        M.new()
      end,
      agent_delete = function(picker, item)
        if not item then return end
        if item.session.id == running then return vim.notify("Agent: can't delete the running session") end
        if vim.fn.confirm(("Delete session %q?"):format(item.session.title), "&Yes\n&No", 2) == 1 then
          os.remove(item.session.path)
          picker:find({ refresh = true })
        end
      end,
    },
    win = { input = { keys = {
      ["<c-n>"] = { "agent_new", mode = { "n", "i" }, desc = "New session" },
      ["<c-x>"] = { "agent_delete", mode = { "n", "i" }, desc = "Delete session" },
    } } },
  })
end

-- Opt-in (vim.g.agent_autostart = true): start Claude with Neovim when it was opened on a folder
-- or a workspace (`nvim`, `nvim .`, `nvp x`), not for single files or git commit messages. Skipped
-- in $HOME and without a UI. Off by default: Space a c starts it when you want it.
function M.autostart()
  vim.api.nvim_create_autocmd("VimEnter", {
    group = vim.api.nvim_create_augroup("claude_autostart", { clear = true }),
    callback = function()
      if vim.g.agent_autostart ~= true or #vim.api.nvim_list_uis() == 0 then return end
      local argc = vim.fn.argc()
      local dir = argc == 1 and vim.fn.isdirectory(vim.fn.argv(0)) == 1 and vim.fn.argv(0)
      if argc > 0 and not dir then return end
      if dir then vim.cmd.cd(vim.fn.fnameescape(dir)) end
      if vim.fn.getcwd() == vim.env.HOME then return end
      vim.schedule(function()
        local win = vim.api.nvim_get_current_win()
        M.start(current_id())
        -- keep the cursor where it was rather than in Claude
        vim.schedule(function()
          if vim.api.nvim_win_is_valid(win) then
            vim.api.nvim_set_current_win(win)
            vim.cmd.stopinsert()
          end
        end)
      end)
    end,
  })
end

function M.setup(opts)
  local group = vim.api.nvim_create_augroup("claude_window", { clear = true })

  vim.api.nvim_create_autocmd({ "BufWinEnter", "TermOpen" }, {
    group = group,
    callback = function(ev)
      -- The buffer may be put into a window other than the current one (the explorer does that)
      if ev.buf ~= claude_buf() then
        for _, win in ipairs(vim.api.nvim_list_wins()) do
          if vim.w[win].claude_pinned and vim.api.nvim_win_get_buf(win) == ev.buf then unpin(win, ev.buf) end
        end
      end
      vim.schedule(function()
        local cw = claude_win()
        if cw then vim.w[cw].claude_pinned = true end
      end)
    end,
  })

  -- Neovim reshares column widths whenever a split opens or closes (e.g. the Space e explorer
  -- next to the Ctrl-/ shell), which drifts Claude's width. Put it back after every change.
  local pct = opts.terminal.split_width_percentage
  vim.api.nvim_create_autocmd({ "WinNew", "WinClosed", "VimResized" }, {
    group = group,
    callback = function()
      vim.schedule(function()
        local win = claude_win()
        if win and #vim.api.nvim_tabpage_list_wins(0) > 1 then
          pcall(vim.api.nvim_win_set_width, win, math.floor(vim.o.columns * pct))
        end
      end)
    end,
  })
end

return M
