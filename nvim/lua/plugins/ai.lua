return {
  -- Claude Code IDE integration: `claude` connects to Neovim over WebSocket like it does to VS Code
  {
    "coder/claudecode.nvim",
    dependencies = { "folke/snacks.nvim" },
    cmd = {
      "ClaudeCode", "ClaudeCodeFocus", "ClaudeCodeSelectModel", "ClaudeCodeAdd", "ClaudeCodeSend",
      "ClaudeCodeSendText", "ClaudeCodeTreeAdd", "ClaudeCodeStatus", "ClaudeCodeStart", "ClaudeCodeStop",
      "ClaudeCodeOpen", "ClaudeCodeClose", "ClaudeCodeDiffAccept", "ClaudeCodeDiffDeny", "ClaudeCodeCloseAllDiffs",
    },
    opts = {
      terminal = {
        provider = "snacks",
        split_side = "right",
        split_width_percentage = 0.35,
        -- One Claude per Neovim: it starts in the cwd of the tab you first open it from (tab-local
        -- :tcd). Remember that dir (review.lua uses it for paths) and say so when toggled elsewhere.
        cwd_provider = function(ctx)
          if not require("claudecode.terminal").get_active_terminal_bufnr() then
            vim.g.claude_cwd = ctx.cwd
          elseif vim.g.claude_cwd and vim.g.claude_cwd ~= ctx.cwd then
            vim.notify("Claude is running in " .. vim.fn.fnamemodify(vim.g.claude_cwd, ":~"), vim.log.levels.INFO)
          end
          return vim.g.claude_cwd or ctx.cwd
        end,
      },
    },
    config = function(_, opts)
      require("claudecode").setup(opts)
      require("agent").setup(opts) -- keeps the Claude window in place (lua/agent.lua)
    end,
    keys = {
      -- Sessions per folder/workspace (lua/agent.lua)
      { "<leader>ac", function() require("agent").toggle() end, desc = "Toggle Claude" },
      { "<leader>an", function() require("agent").new() end, desc = "New Claude session" },
      { "<leader>al", function() require("agent").list() end, desc = "Claude sessions" },
      { "<leader>af", "<cmd>ClaudeCodeFocus<cr>", desc = "Focus Claude" },
      { "<leader>ar", function() require("agent").claude_args("--resume") end, desc = "Resume (Claude's picker)" },
      { "<leader>aC", function() require("agent").claude_args("--continue") end, desc = "Continue last conversation" },
      { "<leader>am", "<cmd>ClaudeCodeSelectModel<cr>", desc = "Select model" },
      { "<leader>ab", "<cmd>ClaudeCodeAdd %<cr>", desc = "Add buffer to Claude" },
      { "<leader>as", "<cmd>ClaudeCodeSend<cr>", mode = "v", desc = "Send selection to Claude" },
      { "<leader>as", "<cmd>ClaudeCodeTreeAdd<cr>", desc = "Add file to Claude", ft = { "snacks_picker_list" } },
      { "<leader>aa", "<cmd>ClaudeCodeDiffAccept<cr>", desc = "Accept diff" },
      { "<leader>ad", "<cmd>ClaudeCodeDiffDeny<cr>", desc = "Deny diff" },
    },
  },

  -- Review what the agent changed: last turn (vs .git/claude-baseline) or everything (vs HEAD)
  {
    "sindrets/diffview.nvim",
    cmd = { "DiffviewOpen", "DiffviewClose", "DiffviewFileHistory", "DiffviewToggleFiles", "DiffviewFocusFiles" },
    opts = {}, -- keymaps (<leader>rv / rV / rq) live in lua/review.lua
  },
}
