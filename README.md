# Promodoro

its pretty easy to build this thing. just build your own. works only on linux due to `pw-play` / `notify-send` dependency and config path. why? becasue its for me.

## usage

```
promo                 open the menu
promo 25m             start a timer right away (quits when you silence it)
promo pomodoro        start a pomodoro right away
promo alarm 07:30     add an alarm right away (7:30pm works too)
promo stop            stop the background daemon (and everything it runs)
```

everything runs in one background daemon (`promo daemon`, started for you the first time you run `promo`). every `promo` you open attaches to it, so you can open it in several terminals and they all show the same pomodoro, timer and alarms. quitting a session just detaches; things keep running and ringing. after rebuilding the binary run `promo stop` so the next `promo` starts the new daemon.

modes:

- **pomodoro**: focus → break → focus … press `space` to start the first focus. when a phase ends the sound loops (any key silences it), a notification is sent, and it waits for `space` before starting the next one. `e` edits the running/next phase length, `f`/`b` edit focus/break.
- **timer**: plain countdown, rings until you press a key. `e` edits its length.
- **alarm**: as many as you want. the list shows them all (`a` add, `x` cancel, `enter` open one to watch it count down). when one rings every session jumps to it and the sound loops until `enter`/`space`, `s` snoozes.
- **settings**: edit the config from the tui, vim style (`j`/`k` move, `i` edit, `esc` normal mode, `space` toggle, `w` save).

length/alarm pickers: `h`/`l` switch hours/minutes, `j`/`k` ±1, `J`/`K` ±10, or just type the digits. `?` shows all keys anywhere.

## config

`~/.config/promo/config.yaml`. missing keys fall back to defaults, and settings writes it back:

```yaml
sound_path: /home/you/Musics/notification.mp3
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
