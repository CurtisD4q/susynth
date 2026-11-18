# Euclidean rhythms with spread, and a chord progression with ticks.
use_bpm 96

live_loop :kick do
  sample :bd_808, amp: 1.2 if spread(3, 8).tick
  sleep 0.25
end

live_loop :clap do
  sync :kick
  sample :perc_snap if spread(2, 8, rotate: 1).tick
  sleep 0.25
end

live_loop :toms do
  sync :kick
  with_fx :echo, phase: 0.375, mix: 0.4 do
    sample choose([:drum_tom_lo_hard, :drum_tom_mid_hard, :drum_tom_hi_hard]) if one_in(3)
  end
  sleep 0.5
end

chords = [chord(:d3, :m7), chord(:g3, :dom7), chord(:c3, :major7), chord(:a2, :m7)]

live_loop :keys do
  sync :kick
  use_synth :prophet
  play chords.ring.tick, release: 3.5, amp: 0.4, cutoff: 90
  sleep 4
end
