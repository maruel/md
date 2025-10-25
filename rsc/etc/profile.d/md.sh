export SHELL
source /opt/git-completion.bash
source /etc/bash_completion.d/git-prompt
GIT_PS1_DESCRIBE_STYLE=tag
GIT_PS1_SHOWCOLORHINTS=1
GIT_PS1_SHOWDIRTYSTATE=1
GIT_PS1_SHOWSTASHSTATE=1
GIT_PS1_SHOWUNTRACKEDFILES=1
GIT_PS1_SHOWUPSTREAM=auto
function __my_ps1 {
  __git_ps1 \
	"\[\e]0;\W\a\]\[\e[0m\]\$(_V=\$?; if [ \$_V != 0 ]; then echo -e -n \"\\[\\e[31m\\]\$_V\\[\\e[0m\\]\" ; fi)" \
	"\[\e[33m\]\w\[\e[0m\]🐳"
}
PROMPT_COMMAND=__my_ps1
export LS_OPTIONS="--color=auto"
eval "$(dircolors)"
alias ls='ls $LS_OPTIONS'
alias ll='ls $LS_OPTIONS -la'
alias vimdiff='nvim -d'
cd /app
