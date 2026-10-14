-- Review agent changes: leave line comments, then send them to the Claude terminal (claudecode.nvim).
--
-- Comments are grouped into reviews. Each repo has at most one active review: new comments go
-- into it and only its comments are shown and sent. Closing a review keeps it in the history
-- (Space rR brings it back); deleting it drops it with its comments.
--
-- Stored in <git dir>/review-comments.json of the file's own repo, so each tab of a multi-repo
-- workspace has its own reviews. Shown as virtual lines above the commented code, which also
-- appear in diffview's right-hand (working tree) buffers since those are the real files.
local M = {}

local ns = vim.api.nvim_create_namespace("review")
local repos = {} -- git dir -> { root, gitdir, reviews, active }
local dir_repo = {} -- dir -> git dir (or false when not in a repo)
local marks = {} -- buf -> { extmark id -> comment }
M.max_inline = 5 -- more open comments than this go to .git/review.md instead of the prompt

local function git(dir, args)
  local r = vim.system(vim.list_extend({ "git", "-C", dir }, args), { text = true }):wait()
  return r.code == 0 and vim.trim(r.stdout) or nil
end

local function load(gitdir, root)
  local repo = { root = root, gitdir = gitdir, reviews = {} }
  local f = io.open(gitdir .. "/review-comments.json")
  if f then
    local ok, data = pcall(vim.json.decode, f:read("*a"))
    f:close()
    if ok and type(data) == "table" then
      repo.reviews, repo.active = data.reviews or {}, data.active
      -- Older files had one flat comment list: turn it into the active review
      if data.comments and #data.comments > 0 then
        table.insert(repo.reviews, { id = #repo.reviews + 1, created = os.time(), comments = data.comments })
        repo.active = #repo.reviews
      end
    end
  end
  return repo
end

local function save(repo)
  local f = assert(io.open(repo.gitdir .. "/review-comments.json", "w"))
  f:write(vim.json.encode({ active = repo.active, reviews = repo.reviews }, { indent = "  " }))
  f:close()
end

local function find_review(repo, id)
  for i, r in ipairs(repo.reviews) do
    if r.id == id then return r, i end
  end
end

local function active(repo) return repo.active and find_review(repo, repo.active) or nil end

-- Active review's comments ({} when there is none)
local function comments(repo)
  local r = active(repo)
  return r and r.comments or {}
end

local function new_review(repo)
  local id = 0
  for _, r in ipairs(repo.reviews) do id = math.max(id, r.id) end
  local r = { id = id + 1, created = os.time(), comments = {} }
  table.insert(repo.reviews, r)
  repo.active = r.id
  return r
end

local function counts(r)
  local n = { open = 0, sent = 0, resolved = 0 }
  for _, c in ipairs(r.comments) do n[c.status] = n[c.status] + 1 end
  return n
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

-- Real file behind a buffer (nil for terminals, diffview:// sides, scratch buffers)
local function buf_file(buf)
  local name = vim.api.nvim_buf_get_name(buf)
  if vim.bo[buf].buftype ~= "" or name == "" or name:match("^%a+://") then return nil end
  return vim.uv.fs_realpath(name) or name
end

local function buf_repo(buf)
  local file = buf_file(buf)
  local repo = repo_at(file and vim.fs.dirname(file) or vim.fn.getcwd())
  return repo, repo and file and file:sub(#repo.root + 2)
end

-- Current repo: the buffer's, else the tab's cwd
function M.repo() return buf_repo(0) end

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

local function reanchor(repo, c, lines)
  local s = M.anchor(lines, c)
  if s ~= c.start_line then
    c.end_line, c.start_line = s + c.end_line - c.start_line, s
    return true
  end
end

function M.render(buf)
  buf = (buf == nil or buf == 0) and vim.api.nvim_get_current_buf() or buf
  if not vim.api.nvim_buf_is_loaded(buf) then return end
  vim.api.nvim_buf_clear_namespace(buf, ns, 0, -1)
  marks[buf] = {}
  if M.hidden then return end
  local repo, rel = buf_repo(buf)
  if not (repo and rel) then return end
  local lines = vim.api.nvim_buf_get_lines(buf, 0, -1, false)
  local moved = false
  for _, c in ipairs(comments(repo)) do
    if c.file == rel and c.status ~= "resolved" then
      moved = reanchor(repo, c, lines) or moved
      local hl = c.status == "sent" and "ReviewCommentSent" or "ReviewComment"
      local virt = {}
      for i, l in ipairs(vim.split(c.text, "\n")) do
        virt[i] = { { (i == 1 and (" 󰆉 #%d%s: "):format(c.id, c.status == "sent" and " (sent)" or "") or "   ") .. l .. " ", hl } }
      end
      local id = vim.api.nvim_buf_set_extmark(buf, ns, c.start_line - 1, 0, {
        end_row = math.min(c.end_line, #lines) - 1,
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
    if vim.api.nvim_buf_is_loaded(buf) and (repo == nil or buf_repo(buf) == repo) then M.render(buf) end
  end
end

function M.add(buf, s, e, text)
  local repo, rel = buf_repo(buf)
  if not (repo and rel) then return vim.notify("Review: not a file in a git repo", vim.log.levels.WARN) end
  local r = active(repo) or new_review(repo)
  local id = 0
  for _, c in ipairs(r.comments) do id = math.max(id, c.id) end
  local c = { id = id + 1, file = rel, start_line = s, end_line = e, text = text, status = "open",
    excerpt = vim.api.nvim_buf_get_lines(buf, s - 1, e, false) }
  table.insert(r.comments, c)
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
  vim.ui.input({ prompt = ("Comment L%d%s: "):format(s, e > s and "-" .. e or "") }, function(text)
    if text and vim.trim(text) ~= "" then M.add(buf, s, e, text) end
  end)
end

local function at_cursor()
  local buf, row = vim.api.nvim_get_current_buf(), vim.fn.line(".") - 1
  for _, m in ipairs(vim.api.nvim_buf_get_extmarks(buf, ns, { row, 0 }, { row, -1 }, { overlap = true })) do
    local c = marks[buf] and marks[buf][m[1]]
    if c then return c end
  end
  vim.notify("Review: no comment on this line", vim.log.levels.WARN)
end

local function update(repo, c, status)
  for i, x in ipairs(comments(repo)) do
    if x == c then
      if status then x.status = status else table.remove(comments(repo), i) end
      break
    end
  end
  save(repo)
  render_repo(repo)
end

function M.delete() local c = at_cursor(); if c then update(M.repo(), c) end end
function M.resolve() local c = at_cursor(); if c then update(M.repo(), c, "resolved") end end

function M.list()
  local repo = M.repo()
  if not repo then return vim.notify("Review: not in a git repo", vim.log.levels.WARN) end
  local r = active(repo)
  if not r then return vim.notify("Review: no active review (Space rR to pick one)") end
  local function act(status)
    return function(picker)
      for _, item in ipairs(picker:selected({ fallback = true })) do update(repo, item.comment, status) end
      picker:find({ refresh = true })
    end
  end
  Snacks.picker.pick({
    title = ("Review %d comments"):format(r.id),
    finder = function()
      local items = {}
      for _, c in ipairs(r.comments) do
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
        { ("%s:%d "):format(c.file, c.start_line), "SnacksPickerFile" }, { c.text } }
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
      ["<c-s>"] = { "review_send", mode = { "n", "i" }, desc = "Send selected to Claude (again)" },
      ["<c-x>"] = { "review_delete", mode = { "n", "i" }, desc = "Delete comment" },
      ["<c-r>"] = { "review_resolve", mode = { "n", "i" }, desc = "Resolve comment" },
      ["<c-o>"] = { "review_reopen", mode = { "n", "i" }, desc = "Mark open again" },
    } } },
  })
end

-- Markdown prompt for a list of comments; paths are relative to `base`
function M.format(repo, comments, base)
  local prefix = base == repo.root and "" or repo.root .. "/"
  local out = { ("I reviewed your changes and left %d comment%s. Please address each one:"):format(#comments, #comments == 1 and "" or "s") }
  for i, c in ipairs(comments) do
    local ft = vim.filetype.match({ filename = c.file }) or ""
    local range = c.end_line > c.start_line and ("%d-%d"):format(c.start_line, c.end_line) or tostring(c.start_line)
    vim.list_extend(out, { "", ("%d. `%s%s:%s`"):format(i, prefix, c.file, range), "```" .. ft })
    vim.list_extend(out, c.excerpt)
    vim.list_extend(out, { "```", c.text })
  end
  return table.concat(out, "\n")
end

-- Send the given comments (open or already sent) to Claude and mark them sent
function M.send_comments(repo, list)
  if #list == 0 then return vim.notify("Review: nothing to send") end
  local open = {}
  for _, c in ipairs(list) do
    reanchor(repo, c, file_lines(repo, c))
    table.insert(open, c)
  end
  local ok, term = pcall(require, "claudecode.terminal")
  if not (ok and term.get_active_terminal_bufnr()) then
    return vim.notify("Review: Claude isn't running (<leader>ac to start it)", vim.log.levels.WARN)
  end
  local base = vim.g.claude_cwd and (vim.uv.fs_realpath(vim.g.claude_cwd) or vim.g.claude_cwd)
  local msg = M.format(repo, open, base)
  if #open > M.max_inline then
    local f = assert(io.open(repo.gitdir .. "/review.md", "w"))
    f:write(msg .. "\n")
    f:close()
    local md = repo.gitdir .. "/review.md"
    if base == repo.root and md:sub(1, #base + 1) == base .. "/" then md = md:sub(#base + 2) end
    msg = ("I left %d review comments on your changes in @%s. Please address each one."):format(#open, md)
  end
  if not term.send_to_terminal(msg) then return end
  for _, c in ipairs(open) do c.status = "sent" end
  save(repo)
  render_repo(repo)
  vim.notify(("Review: sent %d comment%s to Claude"):format(#open, #open == 1 and "" or "s"))
end

local function unresolved(repo, status)
  return vim.tbl_filter(function(c) return c.status ~= "resolved" and (not status or c.status == status) end, comments(repo))
end

-- Send open comments; when everything was already sent, offer to send those again
function M.send()
  local repo = M.repo()
  if not repo then return vim.notify("Review: not in a git repo", vim.log.levels.WARN) end
  local open = unresolved(repo, "open")
  if #open > 0 then return M.send_comments(repo, open) end
  local sent = unresolved(repo, "sent")
  if #sent == 0 then return vim.notify("Review: no comments to send") end
  if vim.fn.confirm(("All %d comments were already sent. Send them again?"):format(#sent), "&Yes\n&No", 2) == 1 then
    M.send_comments(repo, sent)
  end
end

-- Send every unresolved comment of the active review, sent or not
function M.send_all()
  local repo = M.repo()
  if repo then M.send_comments(repo, unresolved(repo)) end
end

local function label(r)
  local n = counts(r)
  return ("Review %d · %s · %d open, %d sent, %d resolved"):format(r.id, os.date("%b %d %H:%M", r.created), n.open, n.sent, n.resolved)
end

-- Close the active review (kept in history); the next comment starts a new one
function M.close()
  local repo = M.repo()
  local r = repo and active(repo)
  if not r then return vim.notify("Review: no active review") end
  repo.active = nil
  save(repo)
  render_repo(repo)
  vim.notify(("Review %d closed. Space rR brings it back."):format(r.id))
end

function M.delete_review(repo, r)
  if vim.fn.confirm(("Delete %s?"):format(label(r)), "&Yes\n&No", 2) ~= 1 then return false end
  local _, i = find_review(repo, r.id)
  table.remove(repo.reviews, i)
  if repo.active == r.id then repo.active = nil end
  save(repo)
  render_repo(repo)
  return true
end

-- Delete the active review and all its comments
function M.clear()
  local repo = M.repo()
  local r = repo and active(repo)
  if not r then return vim.notify("Review: no active review") end
  M.delete_review(repo, r)
end

-- All reviews of this repo: Enter makes one active again, Ctrl-x deletes it, Ctrl-n starts a new one
function M.reviews()
  local repo = M.repo()
  if not repo then return vim.notify("Review: not in a git repo", vim.log.levels.WARN) end
  Snacks.picker.pick({
    title = "Reviews",
    finder = function()
      local items = {}
      for i = #repo.reviews, 1, -1 do
        local r = repo.reviews[i]
        table.insert(items, { review = r, text = label(r) })
      end
      return items
    end,
    format = function(item)
      local r = item.review
      return { { r.id == repo.active and "● " or "  ", "ReviewSign" }, { label(r) } }
    end,
    preview = function(ctx)
      local lines = {}
      for _, c in ipairs(ctx.item.review.comments) do
        table.insert(lines, ("#%d [%s] %s:%d  %s"):format(c.id, c.status, c.file, c.start_line, c.text))
      end
      ctx.preview:set_lines(#lines > 0 and lines or { "(no comments)" })
    end,
    confirm = function(picker, item)
      picker:close()
      if not item then return end
      repo.active = item.review.id
      save(repo)
      render_repo(repo)
      vim.notify(("Review %d is active again (%d comments)"):format(item.review.id, #item.review.comments))
    end,
    actions = {
      review_delete = function(picker, item)
        if item and M.delete_review(repo, item.review) then picker:find({ refresh = true }) end
      end,
      review_new = function(picker)
        picker:close()
        new_review(repo)
        save(repo)
        render_repo(repo)
        vim.notify("Review: started a new review")
      end,
    },
    win = { input = { keys = {
      ["<c-x>"] = { "review_delete", mode = { "n", "i" }, desc = "Delete review and its comments" },
      ["<c-n>"] = { "review_new", mode = { "n", "i" }, desc = "New review" },
    } } },
  })
end

function M.toggle()
  M.hidden = not M.hidden
  render_repo(nil)
  vim.notify("Review comments " .. (M.hidden and "hidden" or "shown"))
end

-- Diff the working tree against the snapshot bin/claude-baseline took when the last prompt was sent
function M.diff_turn()
  local repo = M.repo()
  if not repo then return vim.notify("Review: not in a git repo", vim.log.levels.WARN) end
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

function M.diff_all()
  local repo = M.repo()
  vim.cmd("DiffviewOpen" .. (repo and " -C" .. vim.fn.fnameescape(repo.root) or ""))
end

function M.setup()
  vim.api.nvim_set_hl(0, "ReviewComment", { link = "DiagnosticVirtualTextInfo", default = true })
  vim.api.nvim_set_hl(0, "ReviewCommentSent", { link = "DiagnosticVirtualTextHint", default = true })
  vim.api.nvim_set_hl(0, "ReviewSign", { link = "DiagnosticSignInfo", default = true })
  vim.api.nvim_create_autocmd({ "BufReadPost", "BufWritePost", "FileChangedShellPost", "BufEnter" }, {
    group = vim.api.nvim_create_augroup("review", { clear = true }),
    callback = function(ev) M.render(ev.buf) end,
  })
  local map = vim.keymap.set
  map({ "n", "x" }, "<leader>rc", M.comment, { desc = "Comment line/selection" })
  map("n", "<leader>rl", M.list, { desc = "List comments" })
  map("n", "<leader>rd", M.delete, { desc = "Delete comment" })
  map("n", "<leader>rr", M.resolve, { desc = "Resolve comment" })
  map("n", "<leader>rs", M.send, { desc = "Send comments to Claude" })
  map("n", "<leader>rS", M.send_all, { desc = "Send all unresolved (again)" })
  map("n", "<leader>rR", M.reviews, { desc = "Reviews (reopen / delete)" })
  map("n", "<leader>rn", M.close, { desc = "Close review, start fresh" })
  map("n", "<leader>rX", M.clear, { desc = "Delete review + comments" })
  map("n", "<leader>rt", M.toggle, { desc = "Toggle comment display" })
  map("n", "<leader>rv", M.diff_turn, { desc = "Diff last agent turn" })
  map("n", "<leader>rV", M.diff_all, { desc = "Diff all changes (vs HEAD)" })
  map("n", "<leader>rq", "<cmd>DiffviewClose<cr>", { desc = "Close diff view" })
end

return M
