Output demo.gif

Set Shell bash
Set FontSize 15
Set Width 1100
Set Height 430
Set Padding 16
Set TypingSpeed 35ms
Set Theme "Catppuccin Mocha"

Hide
Type "export PATH=$HOME/src/github.com/kazufusa/oocla/bin:$PATH"
Enter
Type `tmux -f /dev/null -L vhsdemo new-session -d -s d -x 116 -y 24 "env PS1='$ ' bash --noprofile --norc"`
Enter
Type "tmux -L vhsdemo set -g status off"
Enter
Type `tmux -L vhsdemo split-window -h -t d "env PS1='$ ' bash --noprofile --norc"`
Enter
Sleep 1s
Type `tmux -L vhsdemo send-keys -t d:0.0 "oocla serve --addr localhost:11500" Enter`
Enter
Sleep 4s
Type "tmux -L vhsdemo attach -t d"
Enter
Sleep 1s
Show

Type "export OLLAMA_HOST=localhost:11500"
Enter
Sleep 1s
Type `ollama run haiku --think=false "Who made you?"`
Enter
Sleep 14s
