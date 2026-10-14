# Workspaces

A workspace opens several repos at once in Neovim, one tab per repo. Each tab has its own working
directory, so file search, grep, lazygit and git signs follow the repo of the current tab.

Workspace files are personal (they list your own repos), so `*.lua` files in this folder are
ignored by git. Create your own here.

## Example

`nvim/workspaces/shop.lua`:

```lua
-- `nvp shop`: the API and the web app side by side, one tab each
return {
  "~/code/shop-api",
  "~/code/shop-web",
}
```

Then:

```sh
nvp shop    # open it
nvp         # list your workspaces
```

- Paths can use `~`. Paths that don't exist are skipped with a warning.
- The first open builds the tabs from this file. After that, your layout (open files, splits) is
  saved when you quit and restored next time. After changing the list, run `:WorkspaceReset` inside
  Neovim to rebuild the tabs from the file.
- In Neovim: `Space w` switches workspace, `]t` / `[t` move between repo tabs.
- The workspace gets one Claude session, named after it, running in the first repo.
