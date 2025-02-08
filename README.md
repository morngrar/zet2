# zet2 - the new iteration of my unfinished zet command

Sometimes, starting from scratch is better. My previous iteration that was
never made public was getting too abstracted to get anything done, as well as
using unstable dependencies. Back to basics in this one, and it is already
starting to become useful.

Currently, I don't build binaries, so you need go installed for installation.
Install the program with:

```
go install github.com/morngrar/zet2@latest`
```

At version 0.2.0, the tool is starting to get pretty usable in conjunction with
vim as an editor and browser. Below are the remaps I use:

```vim
" general markdown settings
autocmd FileType markdown set wrap
autocmd FileType markdown set linebreak
autocmd FileType markdown set cc=0

" go to zettel ID under cursor
autocmd FileType markdown nnoremap gf :w<cr>:e `zet2 resolve <cfile>`<cr>5j

" go to next zettel link in file
autocmd FileType markdown nnoremap gl /[[<cr>w:noh<cr>

" branch off current zettel and enter edit mode
autocmd FileType markdown nnoremap <leader>zb !!zet2 branch %<cr>:w<cr>k/[[<cr>w:noh<cr>:e `zet2 resolve <cfile>`<cr>5jA

" navigate up in current branch (goes into parent if on first)
autocmd FileType markdown nnoremap <leader>k :w<cr>:noh<cr>:e `zet2 resolve previous path %`<cr>5j

" navigate down in current branch
autocmd FileType markdown nnoremap <leader>j :w<cr>:noh<cr>:e `zet2 resolve next path %`<cr>5j
```
