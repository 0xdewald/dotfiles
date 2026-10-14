-- Multi-repo workspaces: one tab per repo, each with its own tab-local cwd (:tcd),
-- so pickers, grep, lazygit and gitsigns follow the repo of the current tab.
--
-- A workspace is a file in nvim/workspaces/<name>.lua that returns a list of repo paths.
-- First open builds the tabs from that file; after that the layout is saved on exit
-- and restored next time. :WorkspaceReset throws the saved layout away.
local M = {}

local defs_dir = vim.fn.stdpath("config") .. "/workspaces"
local state_dir = vim.fn.stdpath("state") .. "/workspaces"

local function session_file(name) return state_dir .. "/" .. name .. ".vim" end

function M.list()
  local names = {}
  for file in vim.fs.dir(defs_dir) do
    local name = file:match("^(.+)%.lua$")
    if name then table.insert(names, name) end
  end
  table.sort(names)
  return names
end

local function read_def(name)
  local path = defs_dir .. "/" .. name .. ".lua"
  if not vim.uv.fs_stat(path) then
    vim.notify("No workspace '" .. name .. "' (expected " .. path .. ")", vim.log.levels.ERROR)
    return nil
  end
  local repos = {}
  for _, repo in ipairs(dofile(path)) do
    local dir = vim.fs.normalize(repo)
    if vim.fn.isdirectory(dir) == 1 then
      table.insert(repos, dir)
    else
      vim.notify("Workspace '" .. name .. "': skipping missing " .. dir, vim.log.levels.WARN)
    end
  end
  return repos
end

-- Start from a single empty window (drops the dashboard and any previous workspace)
local function clean_slate()
  vim.cmd("silent! tabonly | silent! only | enew")
  for _, buf in ipairs(vim.api.nvim_list_bufs()) do
    if buf ~= vim.api.nvim_get_current_buf() then pcall(vim.api.nvim_buf_delete, buf, { force = true }) end
  end
end

local function build(repos)
  for i, dir in ipairs(repos) do
    if i > 1 then vim.cmd("tabnew") end
    vim.cmd.tcd(vim.fn.fnameescape(dir))
    local readme = vim.fs.find({ "README.md", "readme.md", "README" }, { path = dir, limit = 1 })[1]
    if readme then vim.cmd.edit(vim.fn.fnameescape(readme)) end
  end
  vim.cmd("tabfirst")
end

function M.save()
  local name = vim.g.workspace_name
  if not name then return end
  -- Diffview runs in its own tabs; close those views so they don't come back as plain split tabs
  if package.loaded["diffview"] then
    for _, tab in ipairs(vim.api.nvim_list_tabpages()) do
      for _, win in ipairs(vim.api.nvim_tabpage_is_valid(tab) and vim.api.nvim_tabpage_list_wins(tab) or {}) do
        local buf = vim.api.nvim_win_get_buf(win)
        if vim.api.nvim_buf_get_name(buf):match("^diffview://") or vim.bo[buf].filetype:match("^Diffview") then
          vim.api.nvim_set_current_tabpage(tab)
          pcall(vim.cmd.DiffviewClose)
          break
        end
      end
    end
  end
  -- Explorer, pickers, terminals (incl. Claude) and claudecode's proposed-diff buffers don't
  -- restore cleanly, so leave them out of the session
  for _, win in ipairs(vim.api.nvim_list_wins()) do
    local buf = vim.api.nvim_win_get_buf(win)
    if vim.bo[buf].buftype ~= "" or vim.bo[buf].filetype:match("^snacks") then
      pcall(vim.api.nvim_win_close, win, true)
    end
  end
  vim.fn.mkdir(state_dir, "p")
  vim.cmd("mksession! " .. vim.fn.fnameescape(session_file(name)))
end

function M.open(name)
  if vim.g.workspace_name then M.save() end
  local session = session_file(name)
  if vim.uv.fs_stat(session) then
    clean_slate()
    vim.cmd("silent! source " .. vim.fn.fnameescape(session))
  else
    local repos = read_def(name)
    if not repos then return end
    if #repos == 0 then
      vim.notify("Workspace '" .. name .. "' has no existing repos", vim.log.levels.ERROR)
      return
    end
    clean_slate()
    build(repos)
  end
  vim.g.workspace_name = name
  -- persistence.nvim would otherwise also save a per-directory session on exit
  pcall(function() require("persistence").stop() end)
end

function M.reset()
  local name = vim.g.workspace_name
  if not name then return vim.notify("Not in a workspace", vim.log.levels.WARN) end
  os.remove(session_file(name))
  vim.g.workspace_name = nil
  M.open(name)
end

-- Tabline: one label per tab, named after the tab's repo directory
function M.tabline()
  local parts = {}
  for i, tab in ipairs(vim.api.nvim_list_tabpages()) do
    local hl = tab == vim.api.nvim_get_current_tabpage() and "%#TabLineSel#" or "%#TabLine#"
    local dir = vim.fn.fnamemodify(vim.fn.getcwd(-1, i), ":t")
    table.insert(parts, ("%s%%%dT %d %s %%T"):format(hl, i, i, dir))
  end
  local label = vim.g.workspace_name and ("%#TabLineSel# 󰉋 " .. vim.g.workspace_name .. " ") or ""
  return table.concat(parts) .. "%#TabLineFill#%=" .. label
end

function M.setup()
  vim.o.tabline = "%!v:lua.require'workspace'.tabline()"
  -- No globals/options/terminal in sessions; curdir + tabpages keep each tab's :tcd
  vim.o.sessionoptions = "buffers,curdir,folds,help,tabpages,winsize"

  vim.api.nvim_create_user_command("Workspace", function(o) M.open(o.args) end, {
    nargs = 1,
    complete = function() return M.list() end,
    desc = "Open a multi-repo workspace",
  })
  vim.api.nvim_create_user_command("WorkspaceReset", M.reset, { desc = "Rebuild workspace from its file" })
  vim.api.nvim_create_autocmd("VimLeavePre", { callback = M.save })

  vim.keymap.set("n", "<leader>w", function()
    vim.ui.select(M.list(), { prompt = "Workspace" }, function(name)
      if name then M.open(name) end
    end)
  end, { desc = "Switch workspace" })
  vim.keymap.set("n", "]t", "<cmd>tabnext<cr>", { desc = "Next tab (repo)" })
  vim.keymap.set("n", "[t", "<cmd>tabprevious<cr>", { desc = "Prev tab (repo)" })
end

return M
