return {
  {
    "nvim-treesitter/nvim-treesitter",
    branch = "main",
    lazy = false,
    build = ":TSUpdate",
    config = function()
      require("nvim-treesitter").install({
        "go", "gomod", "gosum", "gowork", "gotmpl",
        "lua", "vim", "vimdoc", "query", "regex",
        "markdown", "markdown_inline", "json", "yaml", "toml",
        "bash", "sql", "proto", "dockerfile", "make",
      })
      vim.api.nvim_create_autocmd("FileType", {
        callback = function(args)
          if pcall(vim.treesitter.start, args.buf) then
            vim.bo[args.buf].indentexpr = "v:lua.require'nvim-treesitter'.indentexpr()"
          end
        end,
      })
    end,
  },

  {
    "neovim/nvim-lspconfig",
    lazy = false,
    config = function()
      vim.lsp.config("gopls", {
        settings = {
          gopls = {
            gofumpt = true,
            usePlaceholders = true,
            completeUnimported = true,
            staticcheck = true,
            semanticTokens = true,
            analyses = { unusedparams = true, unusedwrite = true, nilness = true, shadow = true },
            codelenses = { generate = true, test = true, tidy = true, upgrade_dependency = true },
            hints = {
              assignVariableTypes = true,
              compositeLiteralFields = true,
              constantValues = true,
              functionTypeParameters = true,
              parameterNames = true,
              rangeVariableTypes = true,
            },
          },
        },
      })
      vim.lsp.enable("gopls")

      vim.api.nvim_create_autocmd("LspAttach", {
        callback = function(args)
          local buf = args.buf
          local map = function(l, r, desc) vim.keymap.set("n", l, r, { buffer = buf, desc = desc }) end
          map("<leader>cr", vim.lsp.buf.rename, "Rename")
          map("<leader>ca", vim.lsp.buf.code_action, "Code action")
          map("<leader>cl", vim.lsp.codelens.run, "Run codelens")
          map("<leader>ch", function()
            vim.lsp.inlay_hint.enable(not vim.lsp.inlay_hint.is_enabled({ bufnr = buf }), { bufnr = buf })
          end, "Toggle inlay hints")
          vim.lsp.inlay_hint.enable(true, { bufnr = buf })
        end,
      })
    end,
  },

  {
    "saghen/blink.cmp",
    version = "1.*",
    event = "InsertEnter",
    dependencies = { "rafamadriz/friendly-snippets" },
    opts = {
      keymap = { preset = "super-tab" },
      appearance = { nerd_font_variant = "mono" },
      completion = {
        documentation = { auto_show = true, auto_show_delay_ms = 200 },
        ghost_text = { enabled = true },
        menu = { draw = { treesitter = { "lsp" } } },
      },
      signature = { enabled = true },
      sources = { default = { "lsp", "path", "snippets", "buffer" } },
    },
  },

  {
    "stevearc/conform.nvim",
    event = "BufWritePre",
    cmd = "ConformInfo",
    opts = {
      formatters_by_ft = { go = { "goimports", "gofumpt" }, lua = { "stylua" } },
      format_on_save = { timeout_ms = 1500, lsp_format = "fallback" },
    },
    keys = {
      { "<leader>cf", function() require("conform").format({ async = true }) end, desc = "Format" },
    },
  },

  {
    "mfussenegger/nvim-lint",
    event = { "BufReadPost", "BufWritePost" },
    config = function()
      require("lint").linters_by_ft = { go = { "golangcilint" } }
      vim.api.nvim_create_autocmd({ "BufReadPost", "BufWritePost" }, {
        callback = function() require("lint").try_lint() end,
      })
    end,
  },

  {
    "mfussenegger/nvim-dap",
    dependencies = {
      "leoluz/nvim-dap-go",
      "igorlfs/nvim-dap-view",
      { "theHamsta/nvim-dap-virtual-text", opts = {} },
    },
    keys = {
      { "<leader>db", function() require("dap").toggle_breakpoint() end, desc = "Toggle breakpoint" },
      { "<leader>dc", function() require("dap").continue() end, desc = "Continue / start" },
      { "<leader>do", function() require("dap").step_over() end, desc = "Step over" },
      { "<leader>di", function() require("dap").step_into() end, desc = "Step into" },
      { "<leader>dO", function() require("dap").step_out() end, desc = "Step out" },
      { "<leader>dt", function() require("dap-go").debug_test() end, desc = "Debug nearest test" },
      { "<leader>dq", function() require("dap").terminate() end, desc = "Stop" },
      { "<leader>dv", "<cmd>DapViewToggle<cr>", desc = "Toggle debug view" },
    },
    config = function()
      require("dap-go").setup()
      local dap, dv = require("dap"), require("dap-view")
      dap.listeners.before.attach["dap-view"] = function() dv.open() end
      dap.listeners.before.launch["dap-view"] = function() dv.open() end
      dap.listeners.before.event_terminated["dap-view"] = function() dv.close() end
      dap.listeners.before.event_exited["dap-view"] = function() dv.close() end
      vim.fn.sign_define("DapBreakpoint", { text = "●", texthl = "DiagnosticError" })
      vim.fn.sign_define("DapStopped", { text = "▶", texthl = "DiagnosticOk", linehl = "Visual" })
    end,
  },

  {
    "nvim-neotest/neotest",
    dependencies = {
      "nvim-neotest/nvim-nio",
      "nvim-lua/plenary.nvim",
      "nvim-treesitter/nvim-treesitter",
      { "fredrikaverpil/neotest-golang", version = "*" },
    },
    keys = {
      { "<leader>tt", function() require("neotest").run.run() end, desc = "Run nearest test" },
      { "<leader>tf", function() require("neotest").run.run(vim.fn.expand("%")) end, desc = "Run file tests" },
      { "<leader>tA", function() require("neotest").run.run(vim.uv.cwd()) end, desc = "Run all tests" },
      { "<leader>tl", function() require("neotest").run.run_last() end, desc = "Rerun last test" },
      { "<leader>ts", function() require("neotest").summary.toggle() end, desc = "Test summary" },
      { "<leader>to", function() require("neotest").output.open({ enter = true }) end, desc = "Test output" },
      { "<leader>tw", function() require("neotest").watch.toggle(vim.fn.expand("%")) end, desc = "Watch file" },
    },
    config = function()
      require("neotest").setup({
        adapters = { require("neotest-golang")({ runner = "gotestsum" }) },
      })
    end,
  },
}
