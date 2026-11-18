# Plain Ruby inside Sonic Pi code: methods, hashes, each, map, interpolation.
use_bpm 124

PARTS = {
  intro:  { kick: true,  bass: false },
  groove: { kick: true,  bass: true  }
}

define :part_for do |bar|
  bar < 4 ? :intro : :groove
end

define :bassline do |root|
  [0, 0, 7, 10].map { |i| note(root) + i }
end

live_loop :conductor do
  bar = tick(:bar)
  set :part, part_for(bar)
  puts "bar #{bar + 1}: #{get(:part)}"
  sleep 4
end

live_loop :kick do
  sync :conductor if look(:k).zero?
  tick(:k)
  sample :bd_haus if PARTS[get(:part) || :intro][:kick]
  sleep 1
end

live_loop :bass do
  sync :kick
  if PARTS[get(:part) || :intro][:bass]
    use_synth :dsaw
    bassline(:a1).each do |n|
      play n, release: 0.2, cutoff: 85, amp: 0.7
      sleep 0.25
    end
  else
    sleep 1
  end
end

live_loop :pad do
  sync :conductor
  with_fx :reverb, room: 0.9, mix: 0.6 do
    with_synth :hollow do
      play chord(:a3, :m9), attack: 1, release: 3, amp: 0.3
    end
  end
  sleep 4
end
