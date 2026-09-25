# Promodoro

its pretty easy to build this thing. just build your own. works only on linux due to `pw-play` / `notify-send` dependency and config path. why? becasue its for me.

## usage

```
promo                 open the menu
promo 25m             start a timer right away (quits when done)
promo pomodoro        start a pomodoro right away
promo alarm 07:30     set an alarm right away (7:30pm works too)
```

modes:

- **pomodoro**: focus → break → focus … focus starts right away. when a phase ends it plays the sound, sends a notification and waits for `enter` before starting the next one. `e` edits the running/next phase length, `f`/`b` edit focus/break.
- **timer**: plain countdown. `e` edits its length.
- **alarm**: pick a time, watch it count down. when it rings the sound loops until `enter`/`space`, `s` snoozes.
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
