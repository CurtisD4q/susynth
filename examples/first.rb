# A first set: drums, hats, an acid bass and a pentatonic melody.
use_bpm 120

live_loop :drums do
  sample :bd_haus
  sleep 0.5
  sample :drum_snare_hard, amp: 0.8
  sleep 0.5
end

live_loop :hats do
  sync :drums
  8.times { sample :drum_cymbal_closed, amp: rrand(0.3, 0.6); sleep 0.125 }
end

live_loop :bass do
  sync :drums
  use_synth :tb303
  play (ring :e2, :e2, :g2, :a2).tick, release: 0.2, cutoff: rrand(70, 110)
  sleep 0.25
end

live_loop :melody do
  sync :drums
  use_synth :pluck
  with_fx :reverb, mix: 0.4 do
    play scale(:e4, :minor_pentatonic).choose, amp: 0.6
  end
  sleep 0.5
end
