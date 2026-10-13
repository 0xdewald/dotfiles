vim.g.mapleader = " "
vim.g.maplocalleader = "\\"

-- Go tools (gopls, dlv, gofumpt, ...) live in GOPATH/bin
vim.env.PATH = vim.env.HOME .. "/go/bin:" .. vim.env.PATH

-- Enable both absolute and relative numbers
vim.opt.number = true
vim.opt.relativenumber = true

-- Enable 24 bit color - true collor support that allows hex to work
vim.opt.termguicolors = true

vim.opt.mouse = "a"
vim.opt.clipboard = "unnamedplus"
vim.opt.undofile = true
vim.opt.ignorecase = true
vim.opt.smartcase = true
vim.opt.cursorline = true
vim.opt.scrolloff = 8
vim.opt.splitright = true
vim.opt.splitbelow = true
vim.opt.updatetime = 250
vim.opt.signcolumn = "yes"
vim.opt.winborder = "rounded"
vim.opt.showmode = false

-- Define custom colors for the columns (re-applied after every colorscheme load)
-- Light UI by default; `:set background=dark` switches to the dark theme
vim.o.background = "light"

local function line_nr_colors()
  if vim.o.background == "light" then
    vim.api.nvim_set_hl(0, "AbsoluteLineNr", { fg = "#3b5466", bold = true }) -- Slate for absolute numbers
    vim.api.nvim_set_hl(0, "RelativeLineNr", { fg = "#2e8a4f" })             -- Darker green, readable on light bg
    vim.api.nvim_set_hl(0, "LineNrDivider",  { fg = "#8c8fa1" })             -- Grey for the divider
  else
    vim.api.nvim_set_hl(0, "AbsoluteLineNr", { fg = "#50687b", bold = true }) -- Green for current line
    vim.api.nvim_set_hl(0, "RelativeLineNr", { fg = "#45b86c" })             -- Orange for offset lines
    vim.api.nvim_set_hl(0, "LineNrDivider",  { fg = "#6272a4" })             -- Blue/Grey for the divider
  end
end
line_nr_colors()
vim.api.nvim_create_autocmd("ColorScheme", { callback = line_nr_colors })

-- Build layout using modern NVIM 0.12+ variables
-- %s          : Sign column (git + diagnostics)
-- %{v:lnum}   : Evaluates the current absolute line number
-- %{v:relnum} : Evaluates the relative offset number
vim.opt.statuscolumn = "%s%=%#AbsoluteLineNr#%2{v:lnum}%#LineNrDivider#│%#RelativeLineNr#%2{v:relnum}│ "

vim.diagnostic.config({
  virtual_text = { prefix = "●", spacing = 2 },
  severity_sort = true,
  float = { border = "rounded" },
  signs = {
    text = {
      [vim.diagnostic.severity.ERROR] = " ",
      [vim.diagnostic.severity.WARN] = " ",
      [vim.diagnostic.severity.INFO] = " ",
      [vim.diagnostic.severity.HINT] = "󰌵 ",
    },
  },
})

vim.keymap.set("n", "<Esc>", "<cmd>nohlsearch<CR>")
vim.keymap.set("n", "<C-h>", "<C-w>h")
vim.keymap.set("n", "<C-j>", "<C-w>j")
vim.keymap.set("n", "<C-k>", "<C-w>k")
vim.keymap.set("n", "<C-l>", "<C-w>l")
vim.keymap.set({ "n", "i" }, "<C-s>", "<cmd>w<CR><Esc>", { desc = "Save" })

vim.api.nvim_create_autocmd("TextYankPost", {
  callback = function() vim.hl.on_yank() end,
})

-- Bootstrap lazy.nvim
local lazypath = vim.fn.stdpath("data") .. "/lazy/lazy.nvim"
if not vim.uv.fs_stat(lazypath) then
  vim.fn.system({ "git", "clone", "--filter=blob:none", "--branch=stable",
    "https://github.com/folke/lazy.nvim.git", lazypath })
end
vim.opt.rtp:prepend(lazypath)

require("lazy").setup("plugins", {
  install = { colorscheme = { "catppuccin" } },
  checker = { enabled = true, notify = false },
  change_detection = { notify = false },
})

require("workspace").setup()
