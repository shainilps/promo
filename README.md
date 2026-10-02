# gg

(used to be called promo)

its pretty easy to build this thing. just build your own. works only on linux due to `pw-play` / `notify-send` dependency and config path. why? becasue its for me.

## usage

```
gg                    open the menu
gg 25m                start a timer right away (quits when you silence it)
gg pomodoro           start a pomodoro right away
gg alarm 07:30        add an alarm right away (7:30pm works too)
gg task [list]        open the task lists
gg task add [list]    write tasks in $EDITOR
gg task add work "fix the bug" --time tomorrow-9am
gg task ls [list]     print tasks by date
gg stop               stop the background daemon (and everything it runs)
```

everything runs in one background daemon (`gg daemon`, started for you the first time you run `gg`). every `gg` you open attaches to it, so you can open it in several terminals and they all show the same pomodoro, timer, alarms and tasks. quitting a session just detaches; things keep running and ringing. after rebuilding the binary run `gg stop` so the next `gg` starts the new daemon.

modes:

- **pomodoro**: focus → break → focus … press `space` to start the first focus. when a phase ends the sound loops (any key silences it), a notification is sent, and it waits for `space` before starting the next one. `e` edits the running/next phase length, `f`/`b` edit focus/break.
- **timer**: plain countdown, rings until you press a key. `e` edits its length.
- **alarm**: as many as you want. the list shows them all (`a` add, `x` cancel, `enter` open one to watch it count down). when one rings every session jumps to it and the sound loops until `enter`/`space`, `s` snoozes.
- **tasks**: dated to-dos in lists (`work`, `home`, …). every task belongs to a day; a task with a time is a reminder and you get a notification (and a beep) when it's due. the tasks screen groups them by day, times first. anything unchecked whose time has passed (an earlier reminder today, or any task from a past day) sits in a highlighted OVERDUE block at the top, and every `overdue_nag` (default 1h) you get a notification listing what's still unchecked. keys: `space` check off, `a` add to this list, `A` add to another/new list, `e` edit (text, list, time), `E` open the list's markdown file in `$EDITOR`, `x` delete, `h`/`l` switch list, `c` clear done ones.
- **settings**: edit the config from the tui, vim style (`j`/`k` move, `i` edit, `esc` normal mode, `space` toggle, `w` save).

length/alarm pickers: `h`/`l` switch hours/minutes, `j`/`k` ±1, `J`/`K` ±10, or just type the digits. `?` shows all keys anywhere.

## tasks

`gg task add work "text" --time WHEN` adds one task. no `--time` means today. `WHEN` is a day (`tomorrow`, `9/10/2029`, day/month/year), a time (`13:00`, `5pm`, today), or both joined by `-` (`tomorrow-9am`, `9/10/2029-13:00`). a time makes it a reminder. past times are refused.

`gg task add [list]` opens `$EDITOR` on a draft so you can write a bunch at once:

```markdown
# work
## tomorrow
- [ ] 09:00 standup
- [ ] review the pr
# home
- [ ] call mom
```

`# list` picks the list (a new name makes a new list), `## day` the day, a time in front makes a reminder.

each list is a plain markdown file in `~/.local/share/gg/tasks` (change it with `tasks_dir`). gg keeps them sorted by date, times first:

```markdown
# work

## 2026-10-03 Sat

- [ ] 09:00 standup
- [ ] review the pr
- [x] write the report
```

you can edit them by hand too; the daemon notices within a couple of seconds. (lines that aren't headings or tasks get dropped the next time gg rewrites that file.)

## config

`~/.config/gg/config.yaml` (an old `~/.config/promo` gets moved there). missing keys fall back to defaults, and settings writes it back:

```yaml
sound_path: /home/you/Musics/notification.mp3
tasks_dir: ~/.local/share/gg/tasks
overdue_nag: 1h      # how often to nudge about unchecked past-due tasks; off = never
default_time: 30m
notifications: true
pomodoro:
  work: 25m
  break: 10m
alarm:
  sound_path: ""   # empty = sound_path
  snooze: 5m
ui:
  accent_color: '#CBA6F7'
  bar_start_color: '#89B4FA'   # timer bar; pomodoro/alarm bars follow their mode color
  bar_end_color: '#CBA6F7'
  bar_width: 60
  big_clock: true
  fullscreen: true
  show_help: true
```
