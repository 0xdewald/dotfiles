-- Line comments on code: a comment is a request to change that code, sent to whoever owns it.
--
-- Comment on any line (Space rc): a file, or either side of any diff (last agent turn, uncommitted
-- changes, a range of commits). Comments collect per repo until you send them (Space rs): on a crew
-- agent's branch they go to the board, which hands them to that agent (lua/crew.lua); otherwise to
-- the Claude side panel. Closing a diff or quitting with unsent comments reminds you.
--
-- Stored in <git dir>/review-comments.json of the code's own repo. Shown as virtual lines above the
-- commented code, in files and in diff buffers alike.
local M = {}

local ns = vim.api.nvim_create_namespace("review")
local repos = {} -- git dir -> { root, gitdir, comments }
local dir_repo = {} -- dir -> git dir (or false when not in a repo)
local marks = {} -- buf -> { extmark id -> comment }
M.max_inline = 5 -- more comments than this go to .git/review.md instead of the prompt

local function git(dir, args)
  local r = vim.system(vim.list_extend({ "git", "-C", dir }, args), { text = true }):wait()
  return r.code == 0 and vim.trim(r.stdout) or nil
end

local function load(gitdir, root)
  local repo = { root = root, gitdir = gitdir, comments = {} }
  local f = io.open(gitdir .. "/review-comments.json")
  if f then
    local ok, data = pcall(vim.json.decode, f:read("*a"))
    f:close()
    if ok and type(data) == "table" then
      repo.comments = data.comments or {}
      -- Older files grouped comments into reviews: merge them into one list, renumbered
      for _, r in ipairs(data.reviews or {}) do vim.list_extend(repo.comments, r.comments or {}) end
      for i, c in ipairs(repo.comments) do c.id = i end
    end
  end
  return repo
end

local function save(repo)
  local f = assert(io.open(repo.gitdir .. "/review-comments.json", "w"))
  f:write(vim.json.encode({ comments = repo.comments }, { indent = "  " }))
  f:close()
end

-- Repo for a directory (cached; git is only asked once per dir)
local function repo_at(dir)
  if dir_repo[dir] == nil then
    local out = git(dir, { "rev-parse", "--show-toplevel", "--absolute-git-dir" })
    local root, gitdir = (out or ""):match("^(.-)\n(.+)$")
    dir_repo[dir] = gitdir or false
    if gitdir and not repos[gitdir] then repos[gitdir] = load(gitdir, root) end
  end
  return dir_repo[dir] and repos[dir_repo[dir]] or nil
end

-- A diffview buffer (diffview://…): the repo dir, repo-relative path, commit (nil for the working
-- tree) and side ("old" for the left, pre-change side) of the file it shows
local function diffview_file(buf)
  local ok, lib = pcall(require, "diffview.lib")
  if not ok then return end
  local RevType = require("diffview.vcs.rev").RevType
  for _, view in ipairs(lib.views or {}) do
    local layout = view.cur_entry and view.cur_entry.layout
    for _, f in ipairs(layout and layout:files() or {}) do
      if f.bufnr == buf then
        local commit = f.rev and f.rev.type == RevType.COMMIT and f.rev.commit or nil
        local side = (layout.a and layout.a.file == f) and "old" or "new"
        return f.adapter.ctx.toplevel, f.path, commit, side
      end
    end
  end
end

-- What a buffer shows: repo, repo-relative path, commit (nil = working tree), side ("new"/"old")
local function buf_code(buf)
  local name = vim.api.nvim_buf_get_name(buf)
  if name:match("^diffview://") then
    local top, path, commit, side = diffview_file(buf)
    local repo = top and repo_at(top)
    return repo, repo and path, commit, side
  end
  if vim.bo[buf].buftype ~= "" or name == "" or name:match("^%a+://") then
    return repo_at(vim.fn.getcwd())
  end
  local file = vim.uv.fs_realpath(name) or name
  local repo = repo_at(vim.fs.dirname(file))
  return repo, repo and file:sub(#repo.root + 2), nil, "new"
end

-- Current repo: the buffer's, else the tab's cwd
function M.repo() return (buf_code(0)) end

local function file_lines(repo, c)
  local buf = vim.fn.bufnr(repo.root .. "/" .. c.file)
  if buf > 0 and vim.api.nvim_buf_is_loaded(buf) then return vim.api.nvim_buf_get_lines(buf, 0, -1, false) end
  local ok, lines = pcall(vim.fn.readfile, repo.root .. "/" .. c.file)
  return ok and lines or {}
end

-- Where the comment's code is now: original line if it still matches, else the nearest match of
-- the excerpt, else the original line (clamped)
function M.anchor(lines, c)
  local n = #c.excerpt
  local function at(s)
    for i = 1, n do
      if vim.trim(lines[s + i - 1] or "\0") ~= vim.trim(c.excerpt[i]) then return false end
    end
    return true
  end
  if n > 0 then
    for d = 0, #lines do
      if at(c.start_line + d) then return c.start_line + d end
      if d > 0 and c.start_line - d >= 1 and at(c.start_line - d) then return c.start_line - d end
      if c.start_line + d > #lines and c.start_line - d < 1 then break end
    end
  end
  return math.max(1, math.min(c.start_line, #lines))
end

local function reanchor(c, lines)
  local s = M.anchor(lines, c)
  if s ~= c.start_line then
    c.end_line, c.start_line = s + c.end_line - c.start_line, s
    return true
  end
end

local function where(c)
  local parts = {}
  if c.side == "old" then table.insert(parts, "before the change") end
  if c.commit then table.insert(parts, "in " .. c.commit:sub(1, 10)) end
  return #parts > 0 and (" (" .. table.concat(parts, ", ") .. ")") or ""
end

function M.render(buf)
  buf = (buf == nil or buf == 0) and vim.api.nvim_get_current_buf() or buf
  if not vim.api.nvim_buf_is_loaded(buf) then return end
  vim.api.nvim_buf_clear_namespace(buf, ns, 0, -1)
  marks[buf] = {}
  if M.hidden then return end
  local repo, rel, commit, side = buf_code(buf)
  if not (repo and rel) then return end
  local lines = vim.api.nvim_buf_get_lines(buf, 0, -1, false)
  local moved = false
  for _, c in ipairs(repo.comments) do
    -- old-side comments show on old sides; the rest on new sides and in the file itself
    if c.file == rel and c.status ~= "resolved" and (c.side == "old") == (side == "old") then
      local s
      if commit == nil and not c.commit and side ~= "old" then
        moved = reanchor(c, lines) or moved -- working tree: keep the stored lines current
        s = c.start_line
      else
        s = M.anchor(lines, c)
      end
      local e = s + c.end_line - c.start_line
      local hl = c.status == "sent" and "ReviewCommentSent" or "ReviewComment"
      local virt = {}
      for i, l in ipairs(vim.split(c.text, "\n")) do
        virt[i] = { { (i == 1 and (" 󰆉 #%d%s: "):format(c.id, c.status == "sent" and " (sent)" or "") or "   ") .. l .. " ", hl } }
      end
      local id = vim.api.nvim_buf_set_extmark(buf, ns, s - 1, 0, {
        end_row = math.min(e, #lines) - 1,
        sign_text = "▍",
        sign_hl_group = "ReviewSign",
        virt_lines = virt,
        virt_lines_above = true,
      })
      marks[buf][id] = c
    end
  end
  -- Persist moved anchors only when they match what's on disk
  if moved and not vim.bo[buf].modified then save(repo) end
end

local function render_repo(repo)
  for _, buf in ipairs(vim.api.nvim_list_bufs()) do
    if vim.api.nvim_buf_is_loaded(buf) and (repo == nil or (buf_code(buf)) == repo) then M.render(buf) end
  end
end

function M.add(buf, s, e, text)
  local repo, rel, commit, side = buf_code(buf)
  if not (repo and rel) then return vim.notify("Comment: this buffer isn't code in a git repo", vim.log.levels.WARN) end
  local id = 0
  for _, c in ipairs(repo.comments) do id = math.max(id, c.id) end
  local c = { id = id + 1, file = rel, start_line = s, end_line = e, text = text, status = "open",
    commit = commit, side = side, excerpt = vim.api.nvim_buf_get_lines(buf, s - 1, e, false) }
  table.insert(repo.comments, c)
  save(repo)
  render_repo(repo)
  return c
end

function M.comment()
  local buf, s, e = vim.api.nvim_get_current_buf(), vim.fn.line("."), vim.fn.line(".")
  if vim.fn.mode():match("[vV\22]") then
    s, e = vim.fn.line("v"), vim.fn.line(".")
    if s > e then s, e = e, s end
    vim.cmd("normal! \27")
  end
  vim.ui.input({ prompt = ("Change request L%d%s: "):format(s, e > s and "-" .. e or "") }, function(text)
    if text and vim.trim(text) ~= "" then M.add(buf, s, e, text) end
  end)
end

local function at_cursor()
  local buf, row = vim.api.nvim_get_current_buf(), vim.fn.line(".") - 1
  for _, m in ipairs(vim.api.nvim_buf_get_extmarks(buf, ns, { row, 0 }, { row, -1 }, { overlap = true })) do
    local c = marks[buf] and marks[buf][m[1]]
    if c then return c end
  end
  vim.notify("Comment: none on this line", vim.log.levels.WARN)
end

local function update(repo, c, status)
  for i, x in ipairs(repo.comments) do
    if x == c then
      if status then x.status = status else table.remove(repo.comments, i) end
      break
    end
  end
  save(repo)
  render_repo(repo)
end

function M.delete() local c = at_cursor(); if c then update(M.repo(), c) end end
function M.resolve() local c = at_cursor(); if c then update(M.repo(), c, "resolved") end end

-- Space rl: this repo's open and sent comments
function M.list()
  local repo = M.repo()
  if not repo then return vim.notify("Comment: not in a git repo", vim.log.levels.WARN) end
  local function act(status)
    return function(picker)
      for _, item in ipairs(picker:selected({ fallback = true })) do update(repo, item.comment, status) end
      picker:refresh()
    end
  end
  Snacks.picker.pick({
    title = "Comments · " .. vim.fn.fnamemodify(repo.root, ":t"),
    finder = function()
      local items = {}
      for _, c in ipairs(repo.comments) do
        if c.status ~= "resolved" then
          table.insert(items, { comment = c, file = repo.root .. "/" .. c.file, pos = { c.start_line, 0 },
            text = ("%s %s %s"):format(c.file, c.status, c.text) })
        end
      end
      return items
    end,
    format = function(item)
      local c = item.comment
      return { { ("#%-3d"):format(c.id), "Number" }, { c.status == "sent" and "󰗡 " or "󰆉 ", "ReviewSign" },
        { ("%s:%d"):format(c.file, c.start_line), "SnacksPickerFile" }, { where(c) .. " ", "Comment" }, { c.text } }
    end,
    actions = {
      review_delete = act(nil),
      review_resolve = act("resolved"),
      review_reopen = act("open"),
      review_send = function(picker)
        local sel = vim.tbl_map(function(i) return i.comment end, picker:selected({ fallback = true }))
        picker:close()
        M.send_comments(repo, sel)
      end,
    },
    win = { input = { keys = {
      ["<c-s>"] = { "review_send", mode = { "n", "i" }, desc = "Send selected (again)" },
      ["<c-x>"] = { "review_delete", mode = { "n", "i" }, desc = "Delete comment" },
      ["<c-r>"] = { "review_resolve", mode = { "n", "i" }, desc = "Resolve comment" },
      ["<c-o>"] = { "review_reopen", mode = { "n", "i" }, desc = "Mark open again" },
    } } },
  })
end

-- Markdown prompt for a list of comments; paths are relative to `base`
function M.format(repo, list, base)
  local prefix = base == repo.root and "" or repo.root .. "/"
  local out = { ("I reviewed your changes and left %d comment%s. Please address each one:"):format(#list, #list == 1 and "" or "s") }
  for i, c in ipairs(list) do
    local ft = vim.filetype.match({ filename = c.file }) or ""
    local range = c.end_line > c.start_line and ("%d-%d"):format(c.start_line, c.end_line) or tostring(c.start_line)
    vim.list_extend(out, { "", ("%d. `%s%s:%s`%s"):format(i, prefix, c.file, range, where(c)), "```" .. ft })
    vim.list_extend(out, c.excerpt)
    vim.list_extend(out, { "```", c.text })
  end
  return table.concat(out, "\n")
end

-- Send the given comments (open or already sent) to whoever owns the code, and mark them sent
function M.send_comments(repo, list)
  if #list == 0 then return vim.notify("Comment: nothing to send") end
  for _, c in ipairs(list) do
    if not c.commit and c.side ~= "old" then reanchor(c, file_lines(repo, c)) end
  end
  -- On a crew agent's branch, the board delivers them to that agent (lua/crew.lua)
  local has_crew, crew = pcall(require, "crew")
  if has_crew and crew.route(repo, list) then
    for _, c in ipairs(list) do c.status = "sent" end
    save(repo)
    render_repo(repo)
    return true
  end
  local ok, term = pcall(require, "claudecode.terminal")
  if not (ok and term.get_active_terminal_bufnr()) then
    return vim.notify("Comment: Claude isn't running (Space ac to start it)", vim.log.levels.WARN)
  end
  local base = vim.g.claude_cwd and (vim.uv.fs_realpath(vim.g.claude_cwd) or vim.g.claude_cwd)
  local msg = M.format(repo, list, base)
  if #list > M.max_inline then
    local f = assert(io.open(repo.gitdir .. "/review.md", "w"))
    f:write(msg .. "\n")
    f:close()
    local md = repo.gitdir .. "/review.md"
    if base == repo.root and md:sub(1, #base + 1) == base .. "/" then md = md:sub(#base + 2) end
    msg = ("I left %d review comments on your changes in @%s. Please address each one."):format(#list, md)
  end
  if not term.send_to_terminal(msg) then return end
  for _, c in ipairs(list) do c.status = "sent" end
  save(repo)
  render_repo(repo)
  vim.notify(("Sent %d comment%s to Claude"):format(#list, #list == 1 and "" or "s"))
  return true
end

local function unresolved(repo, status)
  return vim.tbl_filter(function(c) return c.status ~= "resolved" and (not status or c.status == status) end, repo.comments)
end

-- Space rs: send the comments not sent yet; when everything was sent already, offer to resend
function M.send()
  local repo = M.repo()
  if not repo then return vim.notify("Comment: not in a git repo", vim.log.levels.WARN) end
  local open = unresolved(repo, "open")
  if #open > 0 then return M.send_comments(repo, open) end
  local sent = unresolved(repo, "sent")
  if #sent == 0 then return vim.notify("Comment: none to send") end
  if vim.fn.confirm(("All %d comments were already sent. Send them again?"):format(#sent), "&Yes\n&No", 2) == 1 then
    M.send_comments(repo, sent)
  end
end

-- Space rS: send every unresolved comment, sent or not
function M.send_all()
  local repo = M.repo()
  if repo then M.send_comments(repo, unresolved(repo)) end
end

-- Repos (loaded so far) with comments not sent yet: { { repo, count } }
function M.unsent()
  local out = {}
  for _, repo in pairs(repos) do
    local n = #unresolved(repo, "open")
    if n > 0 then table.insert(out, { repo = repo, count = n }) end
  end
  return out
end

-- Who comments in a repo go to: the crew agent on its branch, else your Claude
local function owner(repo)
  local has_crew, crew = pcall(require, "crew")
  local branch = git(repo.root, { "branch", "--show-current" })
  local a = has_crew and crew.available() and branch and branch ~= ""
    and crew.run({ "agent", "show", "--branch", branch, "--repo", vim.fn.fnamemodify(repo.root, ":t") })
  return a and a.name or "your Claude"
end

local function unsent_text(list)
  return table.concat(vim.tbl_map(function(u)
    return ("%d comment%s for %s"):format(u.count, u.count == 1 and "" or "s", owner(u.repo))
  end, list), ", ")
end

-- Reminder when a diff closes with comments not sent
function M.remind()
  local list = M.unsent()
  if #list > 0 then
    vim.notify(unsent_text(list) .. " not sent yet: Space rs", vim.log.levels.WARN)
  end
end

-- Before quitting (Space qq): ask about unsent comments. Returns false to cancel the quit.
function M.confirm_quit()
  local list = M.unsent()
  if #list == 0 then return true end
  local choice = vim.fn.confirm(unsent_text(list) .. " not sent yet.", "&Send\n&Quit anyway\n&Cancel", 1)
  if choice == 3 or choice == 0 then return false end
  if choice == 1 then
    for _, u in ipairs(list) do M.send_comments(u.repo, unresolved(u.repo, "open")) end
  end
  return true
end

function M.toggle()
  M.hidden = not M.hidden
  render_repo(nil)
  vim.notify("Comments " .. (M.hidden and "hidden" or "shown"))
end

-- Repos the current context could mean: where Claude runs, the tab's folder, the open file's repo.
-- They differ when e.g. a tab was switched to another folder with a file of the old one still open.
local function candidates()
  local list, seen = {}, {}
  local function add(repo)
    if repo and not seen[repo] then seen[repo] = true; table.insert(list, repo) end
  end
  if vim.g.claude_cwd then add(repo_at(vim.g.claude_cwd)) end
  add(repo_at(vim.fn.getcwd()))
  add(M.repo())
  return list
end

-- The repo Claude worked in most recently: newest .git/claude-baseline among the candidates
local function turn_repo()
  local best, newest
  for _, repo in ipairs(candidates()) do
    local st = vim.uv.fs_stat(repo.gitdir .. "/claude-baseline")
    if st and (not newest or st.mtime.sec > newest) then best, newest = repo, st.mtime.sec end
  end
  return best
end

-- Diff the working tree against the snapshot bin/claude-baseline took when the last prompt was sent
function M.diff_turn()
  local repo = turn_repo()
  if not repo then
    local c = candidates()[1]
    return vim.notify("Review: no Claude baseline in " .. (c and vim.fn.fnamemodify(c.root, ":~") or "this folder")
      .. " (is the UserPromptSubmit hook set up?)", vim.log.levels.WARN)
  end
  vim.notify("Review: last Claude turn in " .. vim.fn.fnamemodify(repo.root, ":~"))
  local f = io.open(repo.gitdir .. "/claude-baseline")
  local rev = f and vim.trim(f:read("*a"))
  if f then f:close() end
  if not rev or rev == "" or not git(repo.root, { "cat-file", "-e", rev .. "^{commit}" }) then
    return vim.notify("Review: no Claude baseline in " .. repo.root .. " (is the UserPromptSubmit hook set up?)", vim.log.levels.WARN)
  end
  -- diffview hides untracked files when diffing against a commit: mark the ones that appeared
  -- since the baseline (i.e. agent-created) intent-to-add so they show up as added
  local before = {}
  local ok, listed = pcall(vim.fn.readfile, repo.gitdir .. "/claude-baseline-untracked")
  for _, p in ipairs(ok and listed or {}) do before[p] = true end
  local new = vim.tbl_filter(function(p) return p ~= "" and not before[p] end,
    vim.split(git(repo.root, { "ls-files", "--others", "--exclude-standard" }) or "", "\n"))
  if #new > 0 then
    git(repo.root, vim.list_extend({ "add", "-N", "--" }, new))
    vim.notify(("Review: marked %d new file%s intent-to-add"):format(#new, #new == 1 and "" or "s"))
  end
  vim.cmd("DiffviewOpen " .. rev .. " -C" .. vim.fn.fnameescape(repo.root))
end

-- Space rN: review a range of commits. A number n means the last n commits (HEAD~n..HEAD);
-- anything else is a commit or branch to compare HEAD against (e.g. develop, a1b2c3d)
function M.diff_range()
  local repo = candidates()[1]
  if not repo then return vim.notify("Review: not in a git repo", vim.log.levels.WARN) end
  vim.ui.input({ prompt = "Review the last N commits, or since commit/branch: ", default = "1" }, function(input)
    input = input and vim.trim(input)
    if not input or input == "" then return end
    local base = input:match("^%d+$") and ("HEAD~" .. input) or input
    local rev = git(repo.root, { "rev-parse", "--verify", "--quiet", base .. "^{commit}" })
    if not rev then return vim.notify("Review: no commit " .. base .. " in " .. repo.root, vim.log.levels.WARN) end
    local n = git(repo.root, { "rev-list", "--count", base .. "..HEAD" }) or "?"
    vim.notify(("Review: %s commit%s in %s (%s..HEAD)"):format(n, n == "1" and "" or "s",
      vim.fn.fnamemodify(repo.root, ":~"), base))
    vim.cmd("DiffviewOpen " .. vim.fn.fnameescape(base) .. "..HEAD -C" .. vim.fn.fnameescape(repo.root))
  end)
end

function M.diff_all()
  local repo = candidates()[1]
  if repo then vim.notify("Review: all changes in " .. vim.fn.fnamemodify(repo.root, ":~")) end
  vim.cmd("DiffviewOpen" .. (repo and " -C" .. vim.fn.fnameescape(repo.root) or ""))
end

function M.setup()
  vim.api.nvim_set_hl(0, "ReviewComment", { link = "DiagnosticVirtualTextInfo", default = true })
  vim.api.nvim_set_hl(0, "ReviewCommentSent", { link = "DiagnosticVirtualTextHint", default = true })
  vim.api.nvim_set_hl(0, "ReviewSign", { link = "DiagnosticSignInfo", default = true })
  local group = vim.api.nvim_create_augroup("review", { clear = true })
  vim.api.nvim_create_autocmd({ "BufReadPost", "BufWritePost", "FileChangedShellPost", "BufEnter", "BufWinEnter" }, {
    group = group,
    callback = function(ev) M.render(ev.buf) end,
  })
  vim.api.nvim_create_autocmd("User", { group = group, pattern = "DiffviewViewClosed", callback = M.remind })
  local map = vim.keymap.set
  map({ "n", "x" }, "<leader>rc", M.comment, { desc = "Comment (change request)" })
  map("n", "<leader>rl", M.list, { desc = "List comments" })
  map("n", "<leader>rd", M.delete, { desc = "Delete comment" })
  map("n", "<leader>rr", M.resolve, { desc = "Resolve comment" })
  map("n", "<leader>rs", M.send, { desc = "Send comments" })
  map("n", "<leader>rS", M.send_all, { desc = "Send all unresolved (again)" })
  map("n", "<leader>rt", M.toggle, { desc = "Hide/show comments" })
  map("n", "<leader>rv", M.diff_turn, { desc = "Diff last agent turn" })
  map("n", "<leader>rV", M.diff_all, { desc = "Diff all changes (vs HEAD)" })
  map("n", "<leader>rN", M.diff_range, { desc = "Diff a range of commits" })
  map("n", "<leader>rq", "<cmd>DiffviewClose<cr>", { desc = "Close diff view" })
end

return M
