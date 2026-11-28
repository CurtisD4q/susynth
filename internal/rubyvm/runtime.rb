# susynth's Sonic Pi runtime. It runs inside the WebAssembly Ruby sandbox and
# talks to the Go side over stdin/stdout, one tab-separated line per message.
#
# Go -> Ruby                         Ruby -> Go
#   run  id first last at mode nbytes  ran  id first last   (code accepted)
#        + code (mode: swap or pass)
#   kill name                          cue  name
#   chk  nbytes + code                 err  line thread message
#   adv  t                             ev   t thread line kind name k=v,... run
#   stop                               log  text
#   deck (mix in: the playing loops    bpm  value t
#        become the old set)           thr+ / thr- name kind
#   stopold                            ren  name newname
#   syn  names / smp names             .    (end of reply)
#
# Changing a live_loop that is playing (mode swap) starts a new version of it
# from the beginning of the pass the old one is in. The new version runs
# silently until the moment the change was asked for (the next beat or bar),
# then takes over, so it is heard in place, in time. If it fails before that
# moment the old version simply keeps playing; if it fails later, the last
# version that worked comes back, in time. Mode pass is Sonic Pi's way: the
# new code starts on the loop's next pass.
#
# Mixing in a set keeps the loops that are playing as the old set. Their
# names, cues and syncs are kept apart from the new set's, so both can have
# a :drums; the old set's tracks are called old/drums.
#
# Time is in seconds of the engine's sample clock. Every live_loop, in_thread
# and run is a Fiber with its own virtual time; sleep moves that time forward
# and hands control back to the scheduler, which always resumes the thread that
# is furthest behind. Nothing here ever waits on a real clock.

$stdout.binmode
$__out = +""
$__synths = []
$__samples = []
$__sdur = {}
$__warned = {}
$__store = {}

def __send(*f)
  $__out << f.map { |x| x.to_s.tr("\t\n", "  ") }.join("\t") << "\n"
end

def __flush
  $__out << ".\n"
  $stdout.write($__out)
  $stdout.flush
  $__out = +""
end

def __warn_once(key, msg)
  return if $__warned[key]
  $__warned[key] = true
  __send("log", msg, "warn")
end

# ---- threads and the scheduler ------------------------------------------------

class SusynthStop < Exception; end

class SusynthThread
  attr_accessor :name, :kind, :time, :fiber, :ctx, :block, :next_block, :next_ctx,
                :slept, :alive, :wait_from, :wait_for, :seq, :cue_val, :start,
                :deck, :run_id, :next_run, :mute_until, :retire_at, :prev, :block_ctx,
                :good_block, :good_ctx, :good_run, :pass_start, :pass_ticks, :pass_rng,
                :pass_iter, :iteration
end

$__deck = 0      # the set being played; older numbers are the old set
$__swap = true   # mode swap (false: a changed loop starts on its next pass)
$__cue_hist = Hash.new { |h, k| h[k] = [] }

# __silent? is true while a new version of a loop catches up with the old one:
# it runs, but nothing it plays, cues or prints is sent.
def __silent?(th) = !th.nil? && !th.mute_until.nil? && th.time < th.mute_until - 1e-9

# __tname is the name the Go side knows a thread by.
def __tname(th) = th.deck == $__deck ? th.name.to_s : "old/#{th.name}"

$__run = []
$__waiting = Hash.new { |h, k| h[k] = [] }
$__loops = {}
$__seq = 0
$__cur = nil
$__cue_log = false
$__nosleep_hit = false
$__nosleep_line = 0
$__nosleep_left = 0
$__nosleep_n = 0
$__nosleep_t0 = 0.0

# A live_loop that never sleeps never yields its fiber, which used to wedge the
# whole sandbox. While a fiber is resumed, count its own source lines (not the
# runtime's) and abandon just that fiber if it runs on too long. Other loops
# keep their place in the scheduler.
$__guard = TracePoint.new(:line, :call, :b_call) do |tp|
  next if $__nosleep_hit || tp.path == "-e"
  $__nosleep_n += 1
  overdue = $__nosleep_n >= 4_000
  if !overdue && ($__nosleep_n & 15).zero?
    overdue = Process.clock_gettime(Process::CLOCK_MONOTONIC) - $__nosleep_t0 > 0.25
  end
  next unless overdue
  $__nosleep_hit = true
  $__nosleep_line = tp.lineno
  $__nosleep_n = 0
  tp.disable
  throw :susynth_nosleep
end

# An empty `while true; end` never changes line and never calls a method, so
# the trace above does not see it. __arm_spins plants one __tick per pass in
# while/until/for; __tick shares the same budget and abandons the fiber.
def __tick
  $__nosleep_n += 1
  overdue = $__nosleep_n >= 4_000
  if !overdue && ($__nosleep_n & 15).zero?
    overdue = Process.clock_gettime(Process::CLOCK_MONOTONIC) - $__nosleep_t0 > 0.25
  end
  return unless overdue
  $__nosleep_hit = true
  $__nosleep_line = __buffer_line(caller_locations(1, 12))
  $__nosleep_n = 0
  throw :susynth_nosleep
end

def __walk_spins(node, edits)
  return unless node.is_a?(RubyVM::AbstractSyntaxTree::Node)
  case node.type
  when :WHILE, :UNTIL
    __mark_spin(node, node.children[1], edits)
  when :FOR
    __mark_spin(node, node.children[2], edits)
  end
  node.children.each do |c|
    case c
    when RubyVM::AbstractSyntaxTree::Node then __walk_spins(c, edits)
    when Array then c.each { |x| __walk_spins(x, edits) }
    end
  end
end

def __mark_spin(node, _body, edits)
  edits << node.last_lineno
end

def __arm_spins(code)
  ast = __parse(code)
  edits = []
  __walk_spins(ast, edits)
  return code if edits.empty?
  lines = code.lines
  edits.uniq.each do |ln|
    i = ln - 1
    next if i.negative? || i >= lines.size || lines[i].include?("__tick")
    line = lines[i]
    if line.match?(/\bend\b/)
      # block form: the tick sits inside the body, on the end line, so an
      # empty `while true; end` still calls __tick every pass
      lines[i] = line.sub(/^(.*)\bend\b/, "\\1__tick; end")
    elsif (m = line.match(/\A(.*)\b(while|until)\b(.*)\z/))
      lines[i] = "(__tick; #{m[1].strip}) #{m[2]}#{m[3]}"
    end
  end
  lines.join
rescue SyntaxError
  code
end

def __stable_hash(s)
  s.to_s.bytes.reduce(5381) { |h, b| ((h * 33) ^ b) & 0x7fffffff }
end

def __default_ctx
  { bpm: 60.0, synth: :beep, sdef: {}, smpdef: {}, fx: [], transpose: 0, octave: 0,
    rng: Random.new(0), ticks: {}, density: 1.0 }
end

$__main_ctx = __default_ctx

def __ctx
  $__cur ? $__cur.ctx : $__main_ctx
end

def __inherit(c, seed)
  { bpm: c[:bpm], synth: c[:synth], sdef: c[:sdef].dup, smpdef: c[:smpdef].dup, fx: c[:fx].dup,
    transpose: c[:transpose], octave: c[:octave], rng: Random.new(seed), ticks: {}, density: 1.0 }
end

def __buffer_line(locs)
  l = (locs || []).find { |x| x.path == "buffer" }
  l ? l.lineno : 0
end

def __report(e, th)
  line = __buffer_line(e.backtrace_locations)
  if line.zero? && th.block && (loc = th.block.source_location) && loc[0] == "buffer"
    line = loc[1]
  end
  msg = e.message.to_s.lines.first.to_s.strip
  msg = "#{e.class}: #{msg}" unless e.instance_of?(RuntimeError)
  where = th.kind == :loop ? "live_loop :#{th.name}: " : ""
  __send("err", line, __tname(th), where + msg)
end

def __spawn(kind, name, parent, at, ctx: nil, &body)
  th = SusynthThread.new
  th.name, th.kind, th.alive = name, kind, true
  th.time = at
  th.start = at
  th.seq = ($__seq += 1)
  th.deck = parent ? parent.deck : $__deck
  th.run_id = parent&.run_id
  th.mute_until = parent&.mute_until
  th.iteration = 0
  th.ctx = ctx || __inherit(parent ? parent.ctx : $__main_ctx, __stable_hash(name))
  th.fiber = Fiber.new do
    begin
      catch(:susynth_nosleep) { body.call(th) }
      if $__nosleep_hit
        line = $__nosleep_line.to_i
        $__nosleep_hit = false
        where = th.kind == :loop ? "live_loop :#{th.name}: " : ""
        __send("err", line, __tname(th), where + "did not sleep: add a sleep inside the loop")
        __loop_fallback(th) if th.kind == :loop
      end
    rescue SusynthStop
    rescue Exception => e
      __report(e, th)
    end
    :done
  end
  $__run << th
  __send("thr+", __tname(th), kind)
  th
end

def __finish(th)
  return unless th.alive
  th.alive = false
  $__run.delete(th)
  $__waiting.each_value { |list| list.delete(th) }
  key = [th.deck, th.name]
  $__loops.delete(key) if th.kind == :loop && $__loops[key].equal?(th)
  __send("thr-", __tname(th), th.kind)
end

def __nosleep_finish(th)
  return unless th && th.alive
  line = 0
  if th.block && (loc = th.block.source_location) && loc[0] == "buffer"
    line = loc[1]
  end
  where = th.kind == :loop ? "live_loop :#{th.name}: " : ""
  __send("err", line, __tname(th), where + "did not sleep: add a sleep inside the loop")
  __loop_fallback(th) if th.kind == :loop
  __finish(th)
end

def __step(th)
  # an old version of a loop stops where its new version takes over
  if th.retire_at && th.time >= th.retire_at - 1e-9
    __finish(th)
    return
  end
  $__cur = th
  $__nosleep_hit = false
  $__nosleep_n = 0
  $__nosleep_t0 = Process.clock_gettime(Process::CLOCK_MONOTONIC)
  $__guard.enable
  begin
    r = th.fiber.resume
  ensure
    $__guard.disable
    $__cur = nil
  end
  case r
  when :sleep
  when Array # [:sync, name]
    $__run.delete(th)
    th.wait_from = th.time
    $__waiting[r[1]] << th
  else
    __finish(th)
  end
end

def __advance(limit)
  steps = 0
  idle = 0
  $__waiting.values.flatten.each do |w|
    __finish(w) if w.alive && w.retire_at && w.retire_at < limit
  end
  loop do
    # at the same moment, a run goes first: a change timed for a beat must be
    # in place before anything plays on that beat
    th = $__run.min_by { |t| [t.time, t.kind == :run ? 0 : 1, t.seq] }
    break if th.nil? || th.time >= limit
    before = th.time
    catching_up = __silent?(th)
    __step(th)
    steps += 1 unless catching_up
    # sleep 0 yields, so the line guard never sees a long run, but virtual
    # time does not move and this loop would never reach `limit`.
    if th.alive && th.time <= before + 1e-9
      idle += 1
    else
      idle = 0
    end
    if th.alive && idle >= 48
      __nosleep_finish(th)
      idle = 0
    elsif steps >= 4_000
      stuck = $__run.min_by { |t| [t.time, t.kind == :run ? 0 : 1, t.seq] }
      __nosleep_finish(stuck) if stuck && stuck.time < limit
      steps = 0
    end
  end
end

def __kill_named(name)
  name = name.to_s.sub(/\A:/, "")
  list = ($__run + $__waiting.values.flatten).uniq.select { |th| th.alive && __tname(th) == name }
  if list.empty?
    __send("log", ":#{name} is not running")
    return
  end
  list.each { |th| __finish(th) }
end

def __stop_all
  $__main_ctx = __default_ctx
  ($__run + $__waiting.values.flatten).uniq.each { |th| __finish(th) }
  $__run.clear
  $__waiting.clear
  $__loops.clear
  $__last_cue.clear
  $__cue_hist.clear
end

def __threads = ($__run + $__waiting.values.flatten).uniq.select(&:alive)

# __new_deck makes the loops that are playing the old set, ready for a set to
# be mixed in. An old set that is still there stops first.
def __new_deck
  __threads.each { |th| __finish(th) if th.deck < $__deck }
  names = __threads.map { |th| th.name.to_s }.uniq
  $__deck += 1
  names.each { |n| __send("ren", n, "old/#{n}") }
  $__main_ctx = __default_ctx
end

def __stop_old
  __threads.each { |th| __finish(th) if th.deck < $__deck }
end

# Settings made at the top level of a run (use_bpm, use_synth…) also become the
# defaults for later runs, so ^E on a single loop keeps the file's tempo even
# though the use_bpm line wasn't part of that block. A full run resets them.
def __top_set(key, val)
  __ctx[key] = val
  $__main_ctx[key] = val if $__cur && $__cur.kind == :run
  nil
end

def __need_thread(what)
  raise "#{what} only works inside running code" unless $__cur
  $__cur
end

# ---- timing -------------------------------------------------------------------

def sleep(beats)
  th = __need_thread("sleep")
  b = beats.is_a?(Numeric) ? beats.to_f : beats.to_s.to_f
  raise ArgumentError, "sleep needs a number of beats" if b.nan? || b.infinite?
  th.time += b * 60.0 / th.ctx[:bpm] / th.ctx[:density]
  # Sonic Pi lets time go backwards (a negative sleep) and a sleep inside
  # time_warp only moves that block's time: neither hands control back.
  return nil if b.negative? || th.ctx[:warp]
  th.slept = true
  Fiber.yield(:sleep)
  nil
end

def wait(beats) = sleep(beats)

$__last_cue = {}

def sync(*names, **_opts)
  th = __need_thread("sync")
  th.slept = true
  name = names.first.to_s.sub(/\A:/, "").to_sym
  key = [th.deck, name]
  # a cue at exactly this moment counts, whichever thread the scheduler ran first
  if (c = $__last_cue[key]) && (c[0] - th.time).abs < 1e-9 && th.wait_for != [key, c[0]]
    th.wait_for = [key, c[0]]
    th.cue_val = c[1]
    return th.cue_val
  end
  # A version catching up starts in the past: the cues it would have waited
  # for have already happened, so it takes them from the history.
  if __silent?(th) && (c = $__cue_hist[key].find { |ct, _| ct >= th.time - 1e-9 && th.wait_for != [key, ct] })
    th.time = c[0]
    th.wait_for = [key, c[0]]
    th.cue_val = c[1]
    return th.cue_val
  end
  Fiber.yield([:sync, key])
  th.wait_for = [key, th.time]
  th.cue_val
end
alias sync_bpm sync

def __cue(name, val, log)
  th = $__cur
  return nil if __silent?(th)
  t = th ? th.time : 0.0
  name = name.to_s.sub(/\A:/, "").to_sym
  deck = th ? th.deck : $__deck
  key = [deck, name]
  $__last_cue[key] = [t, val]
  hist = $__cue_hist[key]
  hist << [t, val]
  hist.shift while hist.size > 256 || (hist.size > 1 && hist.first[0] < t - 120)
  list = $__waiting.delete(key) || []
  keep = []
  list.each do |w|
    if t >= w.wait_from && w.alive
      w.time = t
      w.cue_val = val
      $__run << w
    else
      keep << w
    end
  end
  $__waiting[key] = keep unless keep.empty?
  __send("cue", deck == $__deck ? name.to_s : "old/#{name}") if log
  nil
end

def cue(name, *args, **kw)
  val = kw.empty? ? (args.size <= 1 ? args.first : args) : kw
  __cue(name, val, true)
end

# stop ends this thread; stop :name ends the loop or thread called name.
def stop(name = nil)
  raise SusynthStop if name.nil?
  __kill_named(name)
  nil
end

def live_loop(name = nil, **opts, &blk)
  unless name.is_a?(Symbol) || name.is_a?(String)
    raise ArgumentError, "live_loop needs a name, like: live_loop :drums do"
  end
  raise ArgumentError, "live_loop :#{name} needs a do ... end block" unless blk
  name = name.to_sym
  parent = __need_thread("live_loop")
  cur = $__loops[[parent.deck, name]]
  if cur && cur.alive
    if !$__swap || cur.pass_start.nil?
      # Sonic Pi's way, and a loop still waiting for its first pass: the new
      # code starts on the next pass.
      cur.next_block, cur.next_ctx, cur.next_run = blk, parent.ctx, parent.run_id
      return nil
    end
    # The new version starts where the old one's pass started and is heard
    # from now (this run starts on the beat or bar the change waits for).
    cur.retire_at = parent.time
    __loop_version(name, blk, parent.ctx, parent, cur.pass_start, prev: cur, mute_until: parent.time)
    return nil
  end
  __loop_version(name, blk, parent.ctx, parent, parent.time, opts: opts)
  nil
end

# __loop_version starts a thread for a live_loop. With prev it continues
# prev's pass: the same ticks, random numbers and pass number.
def __loop_version(name, blk, pctx, parent, at, opts: {}, prev: nil, mute_until: nil)
  th = __spawn(:loop, name, parent, at, ctx: __inherit(pctx, __stable_hash(name))) do |me|
    __loop_body(me, opts)
  end
  th.block, th.block_ctx, th.mute_until, th.prev = blk, pctx, mute_until, prev
  if prev
    th.ctx[:ticks] = prev.pass_ticks.dup if prev.pass_ticks
    th.ctx[:rng] = prev.pass_rng.dup if prev.pass_rng
    th.iteration = prev.pass_iter || 0
    # what prev was playing until now is what to go back to if this fails
    th.good_block, th.good_ctx, th.good_run = prev.block, prev.block_ctx, prev.run_id
  end
  $__loops[[th.deck, name]] = th
  th
end

def __loop_body(me, opts)
  sync(opts[:sync]) if opts[:sync]
  sleep(opts[:delay]) if opts[:delay]
  loop do
    if me.next_block
      me.block, me.block_ctx = me.next_block, me.next_ctx
      me.run_id = me.next_run if me.next_run
      nc = __inherit(me.next_ctx, 0)
      nc[:rng], nc[:ticks] = me.ctx[:rng], me.ctx[:ticks]
      me.ctx = nc
      me.next_block = nil
    end
    me.pass_start = me.time
    me.pass_ticks = me.ctx[:ticks].dup
    me.pass_rng = me.ctx[:rng].dup
    me.pass_iter = me.iteration
    __cue(me.name, nil, $__cue_log)
    me.slept = false
    begin
      me.block.call(me.iteration)
      raise "did not sleep: add a sleep inside the loop" unless me.slept
    rescue SusynthStop
      raise
    rescue Exception => e
      __report(e, me)
      __loop_fallback(me)
      return
    end
    me.iteration += 1
    me.good_block, me.good_ctx, me.good_run = me.block, me.block_ctx, me.run_id
  end
end

# __loop_fallback keeps a loop playing when a new version of it fails: the old
# version carries on if it has not handed over yet, otherwise the last version
# that worked starts again from the failed pass's start and is heard from the
# moment of the error. A version that has worked before and now fails stops,
# as it always did: starting it again would fail again.
def __loop_fallback(me)
  key = [me.deck, me.name]
  return unless $__loops[key].equal?(me)
  pv = me.prev
  if __silent?(me) && pv&.alive
    pv.retire_at = nil
    $__loops[key] = pv
    __send("log", ":#{me.name} kept the old version")
    return
  end
  gb = me.good_block
  return if gb.nil? || gb.equal?(me.block) || me.pass_start.nil?
  at = me.pass_start
  fb = __spawn(:loop, me.name, me, at, ctx: __inherit(me.good_ctx, __stable_hash(me.name))) do |t|
    __loop_body(t, {})
  end
  fb.block, fb.block_ctx, fb.run_id = gb, me.good_ctx, me.good_run
  fb.mute_until = me.time
  fb.ctx[:ticks] = me.pass_ticks.dup
  fb.ctx[:rng] = me.pass_rng.dup
  fb.iteration = me.pass_iter
  fb.good_block, fb.good_ctx, fb.good_run = gb, me.good_ctx, me.good_run
  $__loops[key] = fb
  __send("log", ":#{me.name} went back to the version that worked")
end

def in_thread(name: nil, delay: 0, sync: nil, **_opts, &blk)
  parent = __need_thread("in_thread")
  n = name ? name.to_sym : :thread
  return nil if name && $__run.any? { |t| t.name == n }
  __spawn(:thread, n, parent, parent.time) do
    sync(sync) if sync
    sleep(delay) if delay.to_f > 0
    blk.call
  end
  nil
end

def at(times = [0], values = nil, &blk)
  times = times.is_a?(Numeric) ? [times] : times.to_a
  vals = values&.to_a
  times.each_with_index do |t, i|
    in_thread do
      sleep(t)
      if blk.arity.zero? then blk.call
      elsif vals then blk.call(vals[i % vals.size], i)
      else blk.call(t, i)
      end
    end
  end
  nil
end

def density(d, &blk)
  raise ArgumentError, "density needs a do ... end block" unless blk
  raise ArgumentError, "density must be above 0" unless d.is_a?(Numeric) && d.positive?
  c = __ctx
  old = c[:density]
  c[:density] = old * d.to_f
  reps = d < 1 ? 1 : d.to_i
  reps.times { |i| blk.arity.zero? ? blk.call : blk.call(i) }
  nil
ensure
  c[:density] = old if c
end

def use_bpm(b, &blk)
  raise ArgumentError, "use_bpm does not take a block, use with_bpm" if blk
  unless b.is_a?(Numeric) && b.positive?
    # use_bpm :link, or a tempo read with get before anything set it
    __warn_once("bpm#{b.inspect}", "use_bpm #{b.inspect} isn't a tempo here; keeping #{__ctx[:bpm].round(2)} bpm")
    return nil
  end
  b = b.to_f
  __top_set(:bpm, b)
  __send("bpm", b, format("%.6f", $__cur ? $__cur.time : 0)) if $__cur.nil? || $__cur.kind == :run
  nil
end

def with_bpm(b, &blk)
  c = __ctx
  old = c[:bpm]
  c[:bpm] = b.is_a?(Numeric) && b.positive? ? b.to_f : old
  blk.call
ensure
  c[:bpm] = old if c
end

def current_bpm = __ctx[:bpm]
def vt = $__cur ? $__cur.time - $__cur.start : 0.0
def beat = vt * current_bpm / 60.0
def rt(t) = t.to_f * current_bpm / 60.0
def bt(t) = t.to_f * 60.0 / current_bpm

# ---- notes, chords, scales ------------------------------------------------------

NOTE_BASE = { "c" => 0, "d" => 2, "e" => 4, "f" => 5, "g" => 7, "a" => 9, "b" => 11 }.freeze

def note(n, octave: nil)
  case n
  when nil, :r, :rest, :R then nil
  when Numeric then n
  when Symbol, String
    s = n.to_s.downcase
    return nil if s == "r" || s == "rest"
    m = s.match(/\A([a-g])(s|b|#|f|n)?(-?\d+)?\z/)
    raise ArgumentError, "#{n.inspect} isn't a note (try :c4, :fs3, :eb2 or 60)" unless m
    v = NOTE_BASE[m[1]]
    v += 1 if m[2] == "s" || m[2] == "#"
    v -= 1 if m[2] == "b" || m[2] == "f"
    oct = m[3] ? m[3].to_i : (octave || 4)
    v + (oct + 1) * 12
  else
    raise ArgumentError, "#{n.inspect} isn't a note"
  end
end

def note_info(n) = note(n)
def midi_to_hz(n) = 440.0 * 2**((note(n) - 69) / 12.0)
def hz_to_midi(f) = 69 + 12 * Math.log2(f.to_f / 440.0)

# Sonic Pi's chords (app/server/ruby/lib/sonicpi/chord.rb), plus :power, :sus and :m7b9.
CHORDS = {
  "1" => [0], "5" => [0, 7], "+5" => [0, 4, 8], "m+5" => [0, 3, 8], "sus2" => [0, 2, 7],
  "sus4" => [0, 5, 7], "6" => [0, 4, 7, 9], "m6" => [0, 3, 7, 9], "7sus2" => [0, 2, 7, 10],
  "7sus4" => [0, 5, 7, 10], "7-5" => [0, 4, 6, 10], "halfdiminished" => [0, 3, 6, 10],
  "7+5" => [0, 4, 8, 10], "m7+5" => [0, 3, 8, 10], "9" => [0, 4, 7, 10, 14],
  "m9" => [0, 3, 7, 10, 14], "m7+9" => [0, 3, 7, 10, 14], "maj9" => [0, 4, 7, 11, 14],
  "9sus4" => [0, 5, 7, 10, 14], "9-5" => [0, 4, 6, 10, 14], "6*9" => [0, 4, 7, 9, 14],
  "m6*9" => [0, 3, 7, 9, 14], "7-9" => [0, 4, 7, 10, 13], "m7-9" => [0, 3, 7, 10, 13],
  "7-10" => [0, 4, 7, 10, 15], "7+9" => [0, 4, 7, 10, 15], "7-11" => [0, 4, 7, 10, 16],
  "7-13" => [0, 4, 7, 10, 20], "9+5" => [0, 4, 8, 10, 14], "m9+5" => [0, 3, 8, 10, 14],
  "7+5-9" => [0, 4, 8, 10, 13], "m7+5-9" => [0, 3, 8, 10, 13], "11" => [0, 4, 7, 10, 14, 17],
  "m11" => [0, 3, 7, 10, 14, 17], "maj11" => [0, 4, 7, 11, 14, 17], "11+" => [0, 4, 7, 10, 14, 18],
  "m11+" => [0, 3, 7, 10, 14, 18], "13" => [0, 4, 7, 10, 14, 17, 21],
  "m13" => [0, 3, 7, 10, 14, 17, 21], "maj13" => [0, 4, 7, 11, 14, 17, 21], "add2" => [0, 2, 4, 7],
  "add4" => [0, 4, 5, 7], "add9" => [0, 4, 7, 14], "add11" => [0, 4, 7, 17],
  "add13" => [0, 4, 7, 21], "madd2" => [0, 2, 3, 7], "madd4" => [0, 3, 5, 7],
  "madd9" => [0, 3, 7, 14], "madd11" => [0, 3, 7, 17], "madd13" => [0, 3, 7, 21],
  "major" => [0, 4, 7], "maj" => [0, 4, 7], "M" => [0, 4, 7], "minor" => [0, 3, 7],
  "min" => [0, 3, 7], "m" => [0, 3, 7], "major7" => [0, 4, 7, 11], "maj7" => [0, 4, 7, 11],
  "dom7" => [0, 4, 7, 10], "7" => [0, 4, 7, 10], "M7" => [0, 4, 7, 11], "minor7" => [0, 3, 7, 10],
  "min7" => [0, 3, 7, 10], "m7" => [0, 3, 7, 10], "minor_major7" => [0, 3, 7, 11],
  "mM7" => [0, 3, 7, 11], "mmaj7" => [0, 3, 7, 11], "augmented" => [0, 4, 8], "a" => [0, 4, 8],
  "diminished" => [0, 3, 6], "dim" => [0, 3, 6], "i" => [0, 3, 6], "diminished7" => [0, 3, 6, 9],
  "dim7" => [0, 3, 6, 9], "i7" => [0, 3, 6, 9], "halfdim" => [0, 3, 6, 10],
  "m7b5" => [0, 3, 6, 10], "m7-5" => [0, 3, 6, 10], "minor6" => [0, 3, 7, 9],
  "major6" => [0, 4, 7, 9], "minor9" => [0, 3, 7, 10, 14], "major9" => [0, 4, 7, 11, 14],
  "minor11" => [0, 3, 7, 10, 14, 17], "major11" => [0, 4, 7, 11, 14, 17],
  "minor13" => [0, 3, 7, 10, 14, 17, 21], "power" => [0, 7], "sus" => [0, 5, 7],
  "m7b9" => [0, 3, 7, 10, 13]
}.freeze

# Sonic Pi's scales (app/server/ruby/lib/sonicpi/scale.rb) as steps between notes, plus
# :natural_minor, :kumai and :gong_scale. The Turkish makams use fractions of a semitone.
SCALES = {
  diatonic: [2, 2, 1, 2, 2, 2, 1], ionian: [2, 2, 1, 2, 2, 2, 1], major: [2, 2, 1, 2, 2, 2, 1],
  dorian: [2, 1, 2, 2, 2, 1, 2], phrygian: [1, 2, 2, 2, 1, 2, 2], lydian: [2, 2, 2, 1, 2, 2, 1],
  mixolydian: [2, 2, 1, 2, 2, 1, 2], aeolian: [2, 1, 2, 2, 1, 2, 2], minor: [2, 1, 2, 2, 1, 2, 2],
  locrian: [1, 2, 2, 1, 2, 2, 2], hex_major6: [2, 2, 1, 2, 2, 3], hex_dorian: [2, 1, 2, 2, 3, 2],
  hex_phrygian: [1, 2, 2, 3, 2, 2], hex_major7: [2, 2, 3, 2, 2, 1], hex_sus: [2, 3, 2, 2, 1, 2],
  hex_aeolian: [3, 2, 2, 1, 2, 2], minor_pentatonic: [3, 2, 2, 3, 2], yu: [3, 2, 2, 3, 2],
  major_pentatonic: [2, 2, 3, 2, 3], gong: [2, 2, 3, 2, 3], egyptian: [2, 3, 2, 3, 2],
  shang: [2, 3, 2, 3, 2], jiao: [3, 2, 3, 2, 2], zhi: [2, 3, 2, 2, 3], ritusen: [2, 3, 2, 2, 3],
  whole_tone: [2, 2, 2, 2, 2, 2], whole: [2, 2, 2, 2, 2, 2],
  chromatic: [1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1], harmonic_minor: [2, 1, 2, 2, 1, 3, 1],
  melodic_minor_asc: [2, 1, 2, 2, 2, 2, 1], hungarian_minor: [2, 1, 3, 1, 1, 3, 1],
  octatonic: [2, 1, 2, 1, 2, 1, 2, 1], messiaen1: [2, 2, 2, 2, 2, 2],
  messiaen2: [1, 2, 1, 2, 1, 2, 1, 2], messiaen3: [2, 1, 1, 2, 1, 1, 2, 1, 1],
  messiaen4: [1, 1, 3, 1, 1, 1, 3, 1], messiaen5: [1, 4, 1, 1, 4, 1],
  messiaen6: [2, 2, 1, 1, 2, 2, 1, 1], messiaen7: [1, 1, 1, 2, 1, 1, 1, 1, 2, 1],
  super_locrian: [1, 2, 1, 2, 2, 2, 2], hirajoshi: [2, 1, 4, 1, 4], kumoi: [2, 1, 4, 2, 3],
  neapolitan_major: [1, 2, 2, 2, 2, 2, 1], bartok: [2, 2, 1, 2, 1, 2, 2],
  bhairav: [1, 3, 1, 2, 1, 3, 1], locrian_major: [2, 2, 1, 1, 2, 2, 2],
  ahirbhairav: [1, 3, 1, 2, 2, 1, 2], enigmatic: [1, 3, 2, 2, 2, 1, 1],
  neapolitan_minor: [1, 2, 2, 2, 1, 3, 1], pelog: [1, 2, 4, 1, 4], augmented2: [1, 3, 1, 3, 1, 3],
  scriabin: [1, 3, 3, 2, 3], harmonic_major: [2, 2, 1, 2, 1, 3, 1],
  melodic_minor_desc: [2, 1, 2, 2, 1, 2, 2], romanian_minor: [2, 1, 3, 1, 2, 1, 2],
  hindu: [2, 2, 1, 2, 1, 2, 2], iwato: [1, 4, 1, 4, 2], melodic_minor: [2, 1, 2, 2, 2, 2, 1],
  diminished2: [2, 1, 2, 1, 2, 1, 2, 1], marva: [1, 3, 2, 1, 2, 2, 1],
  melodic_major: [2, 2, 1, 2, 1, 2, 2], indian: [4, 1, 2, 3, 2], spanish: [1, 3, 1, 2, 1, 2, 2],
  prometheus: [2, 2, 2, 5, 1], diminished: [1, 2, 1, 2, 1, 2, 1, 2], todi: [1, 2, 3, 1, 1, 3, 1],
  leading_whole: [2, 2, 2, 2, 2, 1, 1], augmented: [3, 1, 3, 1, 3, 1],
  purvi: [1, 3, 2, 1, 1, 3, 1], chinese: [4, 2, 1, 4, 1], lydian_minor: [2, 2, 2, 1, 1, 2, 2],
  blues_major: [2, 1, 1, 3, 2, 3], blues_minor: [3, 2, 1, 1, 3, 2],
  lydian_dominant: [2, 2, 2, 1, 2, 1, 2], acoustic: [2, 2, 2, 1, 2, 1, 2],
  altered: [1, 2, 1, 2, 2, 2, 2], phrygian_dominant: [1, 3, 1, 2, 1, 2, 2],
  double_harmonic: [1, 3, 1, 2, 1, 3, 1], byzantine: [1, 3, 1, 2, 1, 3, 1],
  cargah: [2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906],
  buselik: [2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  buselik_2: [2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132],
  kurdi: [0.9056603773584906, 2.0377358490566038, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  rast: [2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132],
  acemli_rast: [2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038],
  ussak: [1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  bayati: [1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  bayati_2: [1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  isfahan: [1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  isfahan_2: [1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  hicaz_humayun: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  hicaz_humayun_2: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  hicaz: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038],
  hicaz_2: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038],
  uzzal: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038],
  uzzal_2: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  zirguleli_hicaz: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132],
  zirguleli_hicaz_2: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  huseyni: [1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038],
  huseyni_2: [1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  muhayyer: [1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038],
  gulizar: [1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038],
  neva: [1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038],
  neva_2: [1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038],
  tahir: [1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038],
  tahir_2: [1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038],
  karcigar: [1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038],
  suznak: [2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132],
  suznak_2: [2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  mahur: [2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906],
  acem_asiran: [2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906],
  nihavend: [2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  nihavend_2: [2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132],
  sultani_yegah: [2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  sultani_yegah_2: [2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132],
  kurdili_hicazkar: [0.9056603773584906, 2.0377358490566038, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  kurdili_hicazkar_2: [0.9056603773584906, 2.0377358490566038, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  kurdili_hicazkar_3: [0.9056603773584906, 2.0377358490566038, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038],
  kurdili_hicazkar_4: [0.9056603773584906, 2.0377358490566038, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  kurdili_hicazkar_5: [0.9056603773584906, 2.0377358490566038, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038],
  zirguleli_suznak: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132],
  zirguleli_suznak_2: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  zirguleli_suznak_3: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038],
  hicazkar: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132],
  hicazkar_2: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  evcara: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132],
  evcara_2: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 1.8113207547169812],
  evcara_3: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906],
  evcara_4: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 1.1320754716981132, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132],
  suzidil: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132],
  suzidil_2: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  sedaraban: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132],
  sedaraban_2: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  segah: [1.1320754716981132, 2.0377358490566038, 1.8113207547169812, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132],
  segah_2: [1.1320754716981132, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  huzzam: [1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.716981132075472, 1.1320754716981132],
  huzzam_2: [1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  bayati_araban: [1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  acem_kurdi: [0.9056603773584906, 2.0377358490566038, 2.0377358490566038, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038],
  sehnaz: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038],
  sehnaz_2: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038],
  sehnaz_3: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038],
  sehnaz_4: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132],
  saba: [1.8113207547169812, 1.1320754716981132, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132],
  dugah: [1.8113207547169812, 1.1320754716981132, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132],
  dugah_2: [1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132],
  evic: [1.1320754716981132, 2.0377358490566038, 1.8113207547169812],
  evic_2: [1.1320754716981132, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132],
  bestenigar: [1.1320754716981132, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132],
  ferahnak: [1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.716981132075472, 1.1320754716981132],
  sevkefza: [2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906],
  sevkefza_2: [2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132],
  sevkefza_3: [2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 1.1320754716981132, 2.716981132075472, 1.1320754716981132, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132],
  ferahfeza: [2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038, 1.1320754716981132, 2.716981132075472, 1.1320754716981132],
  ferahfeza_2: [2.0377358490566038, 0.9056603773584906, 2.0377358490566038, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038],
  yegah: [2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 0.9056603773584906, 2.0377358490566038],
  yegah_2: [2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038, 2.0377358490566038, 1.8113207547169812, 1.1320754716981132, 2.0377358490566038],
  natural_minor: [2, 1, 2, 2, 1, 2, 2], kumai: [2, 1, 4, 2, 3], gong_scale: [2, 2, 3, 2, 3]
}.freeze

def chord_names = CHORDS.keys.map(&:to_sym)
def scale_names = SCALES.keys

def chord(root, name = :major, invert: 0, num_octaves: 1, **_opts)
  iv = CHORDS[name.to_s] or raise ArgumentError, "unknown chord #{name.inspect} (try :major :minor :m7 :dom7 :sus4 …)"
  r = note(root)
  notes = (0...num_octaves.to_i).flat_map { |o| iv.map { |i| r + i + 12 * o } }
  ring(*chord_invert(notes, invert))
end

def chord_invert(notes, shift)
  n = notes.to_a.dup
  shift.to_i.abs.times do
    if shift.positive? then n << (n.shift + 12)
    else n.unshift(n.pop - 12)
    end
  end
  ring(*n)
end

def scale(root, name = :major, num_octaves: 1, **_opts)
  iv = SCALES[name.to_sym] or raise ArgumentError, "unknown scale #{name.inspect} (try :major :minor :minor_pentatonic :dorian …)"
  n = note(root)
  out = [n]
  num_octaves.to_i.times { iv.each { |s| out << (n += s) } }
  ring(*out)
end

DEGREES = { i: 1, ii: 2, iii: 3, iv: 4, v: 5, vi: 6, vii: 7, viii: 8 }.freeze

def __degree_index(d)
  d.is_a?(Integer) ? d : (DEGREES[d.to_s.downcase.to_sym] or raise ArgumentError, "unknown degree #{d.inspect}")
end

def degree(d, tonic, sc)
  s = scale(tonic, sc, num_octaves: 3).to_a
  s[__degree_index(d) - 1]
end

def chord_degree(d, tonic, sc = :major, number_of_notes = 4, **_opts)
  s = scale(tonic, sc, num_octaves: 4).to_a
  i = __degree_index(d) - 1
  ring(*(0...number_of_notes).map { |k| s[i + 2 * k] })
end

# ---- rings and helpers ------------------------------------------------------------

class Ring
  include Enumerable
  def initialize(a) = @a = a.to_a
  def [](i, len = nil)
    return Ring.new(@a[i, len] || []) if len
    return Ring.new(@a[i] || []) if i.is_a?(Range)
    @a.empty? ? nil : @a[i.to_i % @a.size]
  end
  def each(&b) = @a.each(&b)
  def size = @a.size
  alias length size
  def to_a = @a.dup
  alias to_ary to_a
  def ring = self
  def tick(k = :___default, **o) = self[tick_counter_advance(k, o)]
  def look(k = :___default, **_o) = self[__ctx[:ticks].fetch(k, 0)]
  def choose = @a.empty? ? nil : @a[__ctx[:rng].rand(@a.size)]
  def pick(n = 1) = Ring.new(Array.new(n) { choose })
  def shuffle = Ring.new(@a.__orig_shuffle(random: __ctx[:rng]))
  def reverse = Ring.new(@a.reverse)
  def rotate(n = 1) = Ring.new(@a.rotate(n))
  def mirror = Ring.new(@a + @a.reverse)
  def reflect = Ring.new(@a + @a.reverse.drop(1))
  def repeat(n = 2) = Ring.new(@a * n.to_i)
  def stretch(n) = Ring.new(@a.flat_map { |x| [x] * n.to_i })
  def take(n) = Ring.new(Array.new(n.to_i) { |i| self[i] })
  def drop(n) = Ring.new(@a.drop(n))
  def butlast = Ring.new(@a[0...-1])
  def butfirst = Ring.new(@a.drop(1))
  def first(n = nil) = n ? Ring.new(@a.first(n)) : @a.first
  def last(n = nil) = n ? Ring.new(@a.last(n)) : @a.last
  # a number moves every note: chord(:e3, :minor) + 12
  def +(o) = o.is_a?(Numeric) ? Ring.new(@a.map { |x| x.nil? ? x : x + o }) : Ring.new(@a + o.to_a)
  def -(o) = o.is_a?(Numeric) ? Ring.new(@a.map { |x| x.nil? ? x : x - o }) : Ring.new(@a - o.to_a)
  def scale(f) = Ring.new(@a.map { |x| x * f })
  def ==(o) = o.respond_to?(:to_a) && @a == o.to_a
  def to_s = "(ring #{@a.map(&:inspect).join(', ')})"
  alias inspect to_s
  def method_missing(m, *a, &b)
    r = @a.respond_to?(m) ? @a.send(m, *a, &b) : super
    r.is_a?(Array) ? Ring.new(r) : r
  end
  def respond_to_missing?(m, p = false) = @a.respond_to?(m, p) || super
end

def tick_counter_advance(k, o = {})
  t = __ctx[:ticks]
  t[k] = t.fetch(k, -1) + (o[:step] || 1)
  t[k] = o[:offset] + t[k] if o[:offset] && t[k] == (o[:step] || 1) - 1
  t[k]
end

class Array
  def ring = Ring.new(self)
  def tick(k = :___default, **o) = Ring.new(self).tick(k, **o)
  def look(k = :___default, **o) = Ring.new(self).look(k, **o)
  def choose = Ring.new(self).choose
end

class Range
  def ring = Ring.new(to_a)
  def tick(*a, **o) = ring.tick(*a, **o)
  def choose = ring.choose
end

def tick(k = :___default, **o) = tick_counter_advance(k.is_a?(Symbol) ? k : k.to_s.to_sym, o)
def look(k = :___default, **_o) = __ctx[:ticks].fetch(k, 0)
def tick_reset(k = :___default) = __ctx[:ticks].delete(k) && nil
def tick_reset_all = __ctx[:ticks].clear && nil
def tick_set(k, v = nil)
  k, v = :___default, k if v.nil?
  __ctx[:ticks][k] = v
  nil
end

def ring(*a) = Ring.new(a)
def knit(*a) = Ring.new(a.each_slice(2).flat_map { |v, n| [v] * n.to_i })
def bools(*a) = Ring.new(a.map { |x| x == true || x == 1 })

def range(s, e, step_or_opts = 1, step: nil, inclusive: false, **_ignored)
  st = (step || (step_or_opts.is_a?(Numeric) ? step_or_opts : 1)).to_f.abs
  raise ArgumentError, "range step must be above 0" if st.zero?
  out = []
  x = s.to_f
  if e >= s
    while inclusive ? x <= e + 1e-9 : x < e - 1e-9
      out << x
      x += st
    end
  else
    while inclusive ? x >= e - 1e-9 : x > e + 1e-9
      out << x
      x -= st
    end
  end
  Ring.new(out.map { |v| v == v.round && s.is_a?(Integer) && st == st.round ? v.round : v })
end

def line(s, f, steps: 4, inclusive: false)
  n = steps.to_i
  return ring(s) if n <= 1
  d = inclusive ? (f - s).to_f / (n - 1) : (f - s).to_f / n
  Ring.new(Array.new(n) { |i| s + d * i })
end

def spread(hits, total, rotate: 0)
  hits, total = hits.to_i, total.to_i
  raise ArgumentError, "spread needs hits ≤ total" if hits > total || total <= 0
  pattern = Array.new(total) { |i| ((i * hits) % total) < hits }
  first = pattern.index(true) || 0
  pattern = pattern.rotate(first)
  rotate.to_i.times do
    pattern = pattern.rotate(1)
    pattern = pattern.rotate(1) until pattern.first || pattern.none?
  end
  Ring.new(pattern)
end

def quantise(n, step) = (n.to_f / step).round * step
alias quantize quantise
def factor?(a, b) = (a % b).zero?

# randomness: deterministic per thread, like Sonic Pi
def rrand(a, b, **_o) = a + __ctx[:rng].rand * (b - a)
def __range_args(r) = [r.begin.to_f, r.end.to_f]
def rrand_i(a, b, **_o) = a.to_i + __ctx[:rng].rand((b.to_i - a.to_i).abs + 1) * (b < a ? -1 : 1)
def rand(m = 1) = m.is_a?(Range) ? rrand(*__range_args(m)) : __ctx[:rng].rand * m
def rand_i(m = 2) = m.is_a?(Range) ? rrand_i(m.begin, m.end) : __ctx[:rng].rand(m.to_i)
def rand_look(m = 1) = Random.new(__ctx[:rng].seed + 1).rand * m
def dice(n = 6) = __ctx[:rng].rand(n.to_i) + 1
def one_in(n) = __ctx[:rng].rand(n.to_i).zero?

# on runs its block only when the condition is true (Sonic Pi's `on`).
def on(cond, &blk) = cond ? blk.call : nil
def choose(list) = list.to_a.empty? ? nil : list.to_a[__ctx[:rng].rand(list.to_a.size)]
# pick with no list is a sample filter: sample "tabla_", pick
def pick(list = nil, n = 1, **_o)
  return ->(names) { [choose(names)] } if list.nil?
  Ring.new(Array.new(n) { choose(list) })
end
def shuffle(list) = Ring.new(list.to_a.shuffle(random: __ctx[:rng]))
def use_random_seed(s) = (__ctx[:rng] = Random.new(s.to_i)) && nil
def with_random_seed(s, &blk)
  c = __ctx
  old = c[:rng]
  c[:rng] = Random.new(s.to_i)
  blk.call
ensure
  c[:rng] = old if c
end
def rand_reset = nil
def rand_skip(_n = 1) = nil

# ---- sound ---------------------------------------------------------------------------

# MOD_FX run inside each voice they cover (internal/synth/mods.go), with the
# kind number the Go side knows them by.
MOD_FX = { slicer: 1, panslicer: 2, tremolo: 3, wobble: 4, ixi_techno: 4, flanger: 5, bpf: 6, rbpf: 6,
           nbpf: 6, nrbpf: 6, band_eq: 7, ring_mod: 8, octaver: 9, vowel: 10, mono: 11, pitch_shift: 12, whammy: 12 }.freeze
FX_SUPPORTED = (%i[reverb gverb echo ping_pong lpf rlpf nlpf nrlpf hpf rhpf nhpf nrhpf bitcrusher krush
                   distortion tanh level pan compressor normaliser eq chorus pitch_shift whammy] + MOD_FX.keys).freeze
# These play the block as it is: recording and audio routing belong to Sonic
# Pi's app, and the autotuner's design is GPL.
FX_DRY = %i[record sound_out sound_out_stereo autotuner].freeze

def __with(key, val, &blk)
  c = __ctx
  old = c[key]
  c[key] = val
  blk.call
ensure
  c[key] = old if c
end

def use_synth(s, &blk)
  raise ArgumentError, "use_synth does not take a block, use with_synth" if blk
  __top_set(:synth, __synth_name(s))
end

def with_synth(s, &blk)
  __with(:synth, __synth_name(s), &blk)
end

def current_synth = __ctx[:synth]
def synth_names = $__synths.map(&:to_sym)
def sample_names(*_a) = Ring.new($__samples.map(&:to_sym))
def all_sample_names = sample_names
def fx_names = FX_SUPPORTED

SOUND_IN = %w[sound_in sound_in_stereo].freeze
SAMPLE_PLAYERS = %w[mono_player stereo_player basic_mono_player basic_stereo_player].freeze

# __synth_name gives the synth to play: the name asked for, or :beep with a
# warning when susynth hasn't got it, so the music carries on.
def __synth_name(s)
  n = s.to_s.sub(/\A:/, "")
  return n.to_sym if $__synths.empty? || $__synths.include?(n) || SOUND_IN.include?(n) || SAMPLE_PLAYERS.include?(n)
  __warn_once("syn#{n}", "synth #{s.inspect} isn't in susynth; :beep plays instead")
  :beep
end

def __check_synth(s) = __synth_name(s)

def use_synth_defaults(**o) = __top_set(:sdef, o)
def use_merged_synth_defaults(**o) = __top_set(:sdef, __ctx[:sdef].merge(o))
def with_synth_defaults(**o, &blk) = __with(:sdef, o, &blk)
def with_merged_synth_defaults(**o, &blk) = __with(:sdef, __ctx[:sdef].merge(o), &blk)
def use_sample_defaults(**o) = __top_set(:smpdef, o)
def use_merged_sample_defaults(**o) = __top_set(:smpdef, __ctx[:smpdef].merge(o))
def with_sample_defaults(**o, &blk) = __with(:smpdef, o, &blk)
def use_transpose(n) = __top_set(:transpose, n)
def with_transpose(n, &blk) = __with(:transpose, n, &blk)
def current_transpose = __ctx[:transpose]
def use_octave(n) = __top_set(:octave, n)
def with_octave(n, &blk) = __with(:octave, n, &blk)

def with_fx(name, reps: 1, **opts, &blk)
  name = name.to_s.sub(/\A:/, "").sub(/\Afx_/, "").to_sym
  opts = opts.dup
  node = SusynthNode.new(:fx, name, nil, [], opts)
  unless FX_SUPPORTED.include?(name)
    msg = FX_DRY.include?(name) ? "does nothing in susynth" : "isn't available in susynth"
    __warn_once("fx#{name}", "with_fx #{name.inspect} #{msg}; the block plays without it")
    r = nil
    reps.to_i.times { r = blk.call(node) }
    return r
  end
  c = __ctx
  c[:fx] = c[:fx] + [[name, opts, $__cur ? $__cur.time : 0.0]]
  r = nil
  reps.to_i.times { r = blk.call(node) }
  r
ensure
  c[:fx] = c[:fx][0...-1] if c && FX_SUPPORTED.include?(name)
end

def __beats(o, k, bpm) = o.key?(k) ? o[k].to_f * 60.0 / bpm : nil

# __fx folds the with_fx stack into per-note settings for the Go side.
def __fx(out, bpm, sample: false, pitch: 0)
  th = $__cur
  mods = 0
  # a sample's own pitch: sits nearest the sound, so it is the first effect
  if sample && pitch.is_a?(Numeric) && pitch != 0
    __mod(out, 0, 12, :pitch_shift, { pitch: pitch }, 0.0, bpm)
    mods = 1
  end
  __ctx[:fx].reverse_each do |name, f, t0|
    next unless (kind = MOD_FX[name])
    next if kind == 12 && !(sample && __shift_semis(name, f) != 0) # a note is transposed instead (below)
    __mod(out, mods, kind, name, f, th ? th.time - t0.to_f : 0.0, bpm)
    mods += 1
  end
  __ctx[:fx].each do |name, f|
    next if sample && %i[pitch_shift whammy].include?(name)
    # pre_amp scales what goes into an effect, and amp what comes out
    out[:amp] = out[:amp].to_f * f[:pre_amp].to_f if f[:pre_amp].is_a?(Numeric)
    out[:amp] = out[:amp].to_f * f[:amp].to_f if f[:amp].is_a?(Numeric)
    case name
    when :pitch_shift, :whammy
      semis = name == :whammy ? (f[:transpose] || 12).to_f : (f[:pitch] || 0).to_f
      out[:pshift] = out[:pshift].to_f + semis * (f[:mix] || 1).to_f.clamp(0, 1).round
    when :reverb, :gverb
      out[:room] = [out[:room].to_f, (f[:mix] || 0.4).to_f].max
      out[:rsize] = (f[:room] || f[:roomsize] || 0.6).to_f
      out[:rdamp] = (f[:damp] || 0.5).to_f
    when :echo, :ping_pong
      phase = (f[:phase] || 0.25).to_f * 60.0 / bpm
      decay = (f[:decay] || 2).to_f * 60.0 / bpm
      out[:dly] = [out[:dly].to_f, (f[:mix] || 1).to_f * 0.6].max
      out[:dtime] = phase
      out[:dfb] = (f[:feedback] || 10**(-3 * phase / [decay, 0.01].max)).to_f.clamp(0, 0.95)
      out[:dpp] = name == :ping_pong ? 1 : 0
    when :lpf, :rlpf, :nlpf, :nrlpf
      c = (f[:cutoff] || 100).to_f
      out[:flpf] = out[:flpf] ? [out[:flpf], c].min : c
      out[:fres] = [out[:fres].to_f, (f[:res] || 0.5).to_f].max if name == :rlpf || name == :nrlpf
    when :hpf, :rhpf, :nhpf, :nrhpf
      c = (f[:cutoff] || 100).to_f
      out[:fhpf] = out[:fhpf] ? [out[:fhpf], c].max : c
      out[:fhres] = [out[:fhres].to_f, (f[:res] || 0.5).to_f].max if name == :rhpf || name == :nrhpf
    when :bitcrusher
      b = (f[:bits] || 8).to_f
      out[:crush] = out[:crush] ? [out[:crush], b].min : b
    when :krush, :distortion, :tanh
      d = (f[:distort] || f[:gain] && (f[:gain].to_f / 10) || 0.5).to_f.clamp(0, 1)
      out[:shape] = [out[:shape].to_f, d].max
    when :compressor
      out[:comp] = 1
      out[:cthr] = (f[:threshold] || 0.2).to_f
      out[:cslope] = (f[:slope_above] || 0.5).to_f
      out[:cclamp] = (f[:clamp_time] || 0.01).to_f
      out[:crelax] = (f[:relax_time] || 0.1).to_f
    when :eq
      out[:eq] = 1
      { low_shelf: :eqlo, low_shelf_note: :eqlon, mid: :eqmid, mid_note: :eqmidn, mid_q: :eqq,
        high_shelf: :eqhi, high_shelf_note: :eqhin }.each { |k, s| out[s] = f[k].to_f if f.key?(k) }
    when :chorus
      out[:cho] = [out[:cho].to_f, (f[:mix] || 0.5).to_f].max
      out[:chrate] = (f[:rate] || 0.6).to_f
      out[:chdepth] = (f[:depth] || 2.5).to_f
    when :pan
      out[:pan] = (f[:pan] || 0).to_f
    end
  end
  out
end

def __shift_semis(name, f) = (name == :whammy ? (f[:transpose] || 12) : (f[:pitch] || 0)).to_f

# __mod adds one per-voice effect (internal/synth/mods.go) to a note's
# settings as x<i><field>=value. Times go to seconds; the LFO's origin is
# how long the effect has been running.
def __mod(out, i, kind, name, f, origin, bpm)
  beat = 60.0 / bpm
  num = ->(k, d) { (v = f[k]).is_a?(Numeric) ? v.to_f : (v == true ? 1.0 : (v == false ? 0.0 : d)) }
  x = { k: kind, mix: num.(:mix, 1), org: origin,
        off: num.(:phase_offset, 0), inv: num.(:invert_wave, 0) }
  case name
  when :slicer, :panslicer
    x[:ph] = num.(:phase, 0.25) * beat
    x[:wv] = num.(:wave, 1)
    x[:w] = num.(:pulse_width, 0.5)
    x[:sm] = [num.(:smooth, 0), num.(:smooth_up, 0), num.(:smooth_down, 0)].max * beat
    x[:pb] = num.(:probability, 0)
    x[:sd] = num.(:seed, 0)
    if name == :slicer
      x[:lo], x[:hi], x[:pp] = num.(:amp_min, 0), num.(:amp_max, 1), num.(:prob_pos, 0)
    else
      lo, hi = num.(:pan_min, -1), num.(:pan_max, 1)
      x[:lo], x[:hi], x[:pp] = lo, hi, (num.(:prob_pos, 0) + 1) / 2
    end
  when :tremolo
    d = num.(:depth, 0.5).clamp(0, 1)
    x.merge!(ph: num.(:phase, 4) * beat, wv: num.(:wave, 2), lo: 1 - d, hi: 1)
  when :wobble, :ixi_techno
    wob = name == :wobble
    x.merge!(ph: num.(:phase, wob ? 0.5 : 4) * beat, wv: wob ? num.(:wave, 0) : 3,
             w: num.(:pulse_width, 0.5), lo: num.(:cutoff_min, 60), hi: num.(:cutoff_max, 120),
             res: num.(:res, 0.8), sm: num.(:smooth, 0) * beat, pb: num.(:probability, 0),
             pp: num.(:prob_pos, 0), sd: num.(:seed, 0))
    x[:off] += 0.5 unless wob # ixi_techno's sine starts at the bottom
  when :flanger
    x.merge!(ph: num.(:phase, 4) * beat, wv: [num.(:wave, 4), 3].min, dl: num.(:delay, 5),
             dp: num.(:depth, 5), fb: num.(:feedback, 0).clamp(0, 0.95))
  when :bpf, :rbpf, :nbpf, :nrbpf
    x.merge!(fq: num.(:centre, num.(:cutoff, 100)), res: num.(:res, name == :rbpf || name == :nrbpf ? 0.5 : 0.6))
  when :band_eq
    x.merge!(fq: num.(:freq, 100), res: num.(:res, 0.6), db: num.(:db, 0.6))
  when :ring_mod
    x.merge!(fq: num.(:freq, 30))
    x[:mix] = x[:mix] * num.(:mod_amp, 1).clamp(0, 1)
  when :octaver
    x.merge!(a: num.(:super_amp, 1), b: num.(:sub_amp, 1), c: num.(:subsub_amp, 1))
  when :vowel
    x.merge!(a: num.(:vowel_sound, 1), b: num.(:voice, 0))
  when :pitch_shift, :whammy
    x.merge!(a: __shift_semis(name, f), dl: (num.(:window_size, 0.2) * 500).clamp(30, 200))
  end
  x.each { |k, v| out[:"x#{i}#{k}"] = v }
end

def __emit(kind, name, o, delay = 0.0)
  th = __need_thread(kind == "s" ? "sample" : "play")
  line = __buffer_line(caller_locations(2, 12))
  kv = o.map { |k, v| "#{k}=#{v.is_a?(Float) ? v.round(5) : v}" }.join(",")
  return if __silent?(th)
  __send("ev", format("%.6f", th.time + delay), __tname(th), line, kind, name, kv, th.run_id.to_i)
end

# A node stands for a sound that has started (a note, a sample or an effect),
# so code that keeps hold of one (s = play 60 ... control s, note: 65) runs.
class SusynthNode
  attr_reader :kind, :name, :t_end, :ids, :opts
  def initialize(kind, name, t_end, ids = [], opts = nil)
    @kind, @name, @t_end, @ids, @opts = kind, name, t_end, ids, opts
  end
  def control(**o) = Object.send(:control, self, **o)
  def kill(**_o) = Object.send(:kill, self)
  def pause(**_o) = nil
  def run(**_o) = nil
  def live? = $__cur.nil? || @t_end.nil? || $__cur.time < @t_end
  alias alive? live?
  alias running? live?
  def destroyed? = !live?
  def paused? = false
  def to_s = "(node #{@kind} #{@name})"
  alias inspect to_s
end

def play(n = :__none, *rest, **opts, &_blk)
  opts = rest.first.merge(opts) if rest.first.is_a?(Hash)
  if n.is_a?(Hash)
    opts = n.merge(opts)
    n = :__none
  end
  if n.is_a?(Array) || n.is_a?(Ring)
    nodes = n.to_a.map { |x| play(x, **opts) }.compact
    return nil if nodes.empty?
    return SusynthNode.new(:synth, nodes.first.name, nodes.map(&:t_end).compact.max, nodes.flat_map(&:ids))
  end
  n = 52 if n == :__none
  c = __ctx
  o = c[:sdef].merge(opts)
  return nil if o.key?(:on) && !o[:on]
  sn = o.delete(:synth) || c[:synth]
  if SAMPLE_PLAYERS.include?(sn.to_s)
    # mono_player and stereo_player play a sample file through synth
    return nil unless (path = o[:path])
    return sample(path, **o.except(:path, :note, :on))
  end
  s = __synth_name(sn)
  if SOUND_IN.include?(s.to_s)
    __warn_once("sndin", "synth #{s.inspect} needs a live audio input, which susynth hasn't got; it is skipped")
    return nil
  end
  nn = note(o.key?(:note) ? o.delete(:note) : n)
  return nil if nn.nil?
  nn += c[:transpose] + c[:octave] * 12
  bpm = c[:bpm]
  ev = { n: nn.to_f, amp: (o[:amp] || 1).to_f, pan: (o[:pan] || 0).to_f }
  %i[attack decay sustain release].each { |k| (v = __beats(o, k, bpm)) && ev[k.to_s[0].to_sym] = v }
  ev[:r] ||= 60.0 / bpm unless o.key?(:release)
  { attack_level: :al, decay_level: :dl, sustain_level: :sl,
    cutoff_attack_level: :cal, cutoff_decay_level: :cdl, cutoff_sustain_level: :csl }.each do |k, s|
    ev[s] = o[k].to_f if o.key?(k)
  end
  { cutoff_attack: :ca, cutoff_decay: :cd, cutoff_sustain: :cs, cutoff_release: :cr }.each do |k, s|
    (v = __beats(o, k, bpm)) && ev[s] = v
  end
  ev[:cmin] = o[:cutoff_min].to_f if o.key?(:cutoff_min)
  ev[:cutoff] = o[:cutoff].to_f if o[:cutoff]
  ev[:res] = o[:res].to_f if o[:res]
  __synth_opts(ev, s.to_s, o, bpm)
  __fx(ev, bpm)
  ev[:n] += ev.delete(:pshift).to_f if ev.key?(:pshift)
  id = __node_id
  ev[:id] = id
  __slides(o, ev, bpm)
  __emit("p", s, ev)
  th = $__cur
  SusynthNode.new(:synth, s, th && th.time + ev[:a].to_f + ev[:d].to_f + ev[:s].to_f + ev[:r].to_f, [id])
end

def synth(name, *rest, **opts, &blk)
  opts = rest.first.merge(opts) if rest.first.is_a?(Hash)
  return play(**opts, &blk) if name.nil?
  play(opts.key?(:note) ? opts[:note] : 52, synth: name, **opts.except(:note))
end

# SYNTH_OPTS are the synth-specific options the voices read, as the event
# key each goes under.
SYNTH_OPTS = { detune: :det, divisor: :div, depth: :dep, vel: :vel, vibrato_rate: :vr, vibrato_depth: :vd,
               vibrato_delay: :vdl, vibrato_onset: :von }.freeze

# The mod_ synths sweep their note up by mod_range semitones on an
# oscillator (a pulse by default) one mod_phase beats long.
def __synth_opts(ev, synth, o, bpm)
  SYNTH_OPTS.each { |k, d| ev[d] = o[k].to_f if o[k].is_a?(Numeric) }
  return unless synth.start_with?("mod_")
  ev[:mr] = (o[:mod_range] || 5).to_f
  ev[:mp] = (o[:mod_phase] || 0.25).to_f * 60.0 / bpm
  ev[:mw] = (o[:mod_wave] || 1).to_i
  ev[:mpw] = (o[:mod_pulse_width] || 0.5).to_f
  ev[:moff] = (o[:mod_phase_offset] || 0).to_f
  ev[:minv] = o[:mod_invert_wave].to_f if o[:mod_invert_wave]
end

def play_chord(notes, **opts) = play(notes.to_a, **opts)

def play_pattern(notes, **opts)
  notes.to_a.each do |n|
    play(n, **opts)
    sleep 1
  end
  nil
end

def play_pattern_timed(notes, times, **opts)
  ts = times.is_a?(Numeric) ? [times] : times.to_a
  notes.to_a.each_with_index do |n, i|
    play(n, **opts)
    sleep ts[i % ts.size]
  end
  nil
end

SAMPLE_EXT = /\.(wav|wave|aif|aiff|flac|ogg|oga|mp3)\z/i

# __sample_path? is true for a string that names a file or folder rather
# than filtering the built-in sample names.
def __sample_path?(s) = s.include?("/") || s.include?("\\") || s.start_with?("~") || s.match?(SAMPLE_EXT)

# __sample_filter picks a built-in sample the way Sonic Pi's filters do:
# every string or symbol must be part of the name, a number picks the nth
# match and a proc (or lambda) narrows the list.
def __sample_filter(args)
  idx = 0
  list = $__samples.sort
  args.each do |a|
    case a
    when Integer then idx = a
    when String, Symbol then list = list.select { |n| n.include?(a.to_s) }
    when Regexp then list = list.select { |n| n.match?(a) }
    when Proc then list = Array(a.call(list)).map(&:to_s)
    end
  end
  list.empty? ? nil : list[idx % list.size]
end

def sample(name = nil, *rest, **opts, &_blk)
  idx = rest.first.is_a?(Integer) ? rest.first : nil
  c = __ctx
  o = c[:smpdef].merge(opts)
  return nil if o.key?(:on) && !o[:on]
  name = name.first if name.is_a?(Array) && name.size == 1
  case name
  when Symbol
    n = name.to_s
    if !$__samples.empty? && !$__samples.include?(n)
      __warn_once("smp#{n}", "sample #{name.inspect} isn't in susynth yet; it is skipped")
      return nil
    end
  when String
    if __sample_path?(name) && rest.none? { |a| a.is_a?(String) || a.is_a?(Proc) || a.is_a?(Symbol) }
      n = name
    else
      args = (__sample_path?(name) ? [] : [name]) + rest
      n = __sample_filter(args)
      idx = nil
      unless n
        __warn_once("smpf#{args.inspect}", "no built-in sample matches #{args.map(&:inspect).join(', ')}; it is skipped")
        return nil
      end
    end
  when nil
    return nil
  else raise ArgumentError, "sample needs a name like :bd_haus or a path to a .wav file"
  end
  bpm = c[:bpm]
  st, fi = __sample_region(o)
  ev = { amp: (o[:amp] || 1).to_f * (o[:pre_amp] || 1).to_f, pan: (o[:pan] || 0).to_f, rate: __sample_rate(n, o, bpm) }
  ev[:st] = st.round(6) if st != 0
  ev[:fi] = fi.round(6) if fi != 1
  ev[:i] = idx if idx
  cut = o[:cutoff] || o[:lpf]
  ev[:cutoff] = cut.to_f if cut.is_a?(Numeric)
  ev[:hpf] = o[:hpf].to_f if o[:hpf].is_a?(Numeric)
  ev[:res] = o[:res].to_f if o[:res]
  %i[attack decay sustain release].each { |k| (v = __beats(o, k, bpm)) && ev[k.to_s[0].to_sym] = v }
  ev.delete(:s) if ev[:s] && ev[:s] < 0 # sustain: -1 is Sonic Pi's "to the end"
  __warn_once("smpps", "sample pitch_stretch: changes the pitch too in susynth") if o[:pitch_stretch]
  __warn_once("smpon", "sample onset: isn't in susynth; the whole sample plays") if o[:onset]
  __fx(ev, bpm, sample: true, pitch: o[:pitch])
  id = __node_id
  ev[:id] = id
  __slides(o, ev, bpm)
  __emit("s", n, ev)
  SusynthNode.new(:sample, n, nil, [id])
end

# __sample_region is the part of a sample to play, as fractions: start:,
# finish:, and slice: of num_slices: (16 by default).
def __sample_region(o)
  st = (o[:start] || 0).to_f
  fi = (o[:finish] || 1).to_f
  if o.key?(:slice) && !o[:slice].nil?
    n = (o[:num_slices] || 16).to_i
    n = 16 if n <= 0
    sl = o[:slice]
    sl = __ctx[:rng].rand(n) if sl == :rand
    sl = sl.to_i % n
    a, w = sl.to_f / n, 1.0 / n
    st, fi = a + st * w, a + fi * w
  end
  [st.clamp(0.0, 1.0), fi.clamp(0.0, 1.0)]
end

# __sample_rate is the speed to play at: rate:, rpitch: (semitones), and
# beat_stretch: (fit the whole sample to that many beats).
def __sample_rate(n, o, bpm)
  r = (o[:rate] || 1).to_f
  r *= 2**(o[:rpitch].to_f / 12) if o[:rpitch]
  bs = o[:beat_stretch] || o[:pitch_stretch]
  if bs && bs.to_f > 0 && (d = __sample_seconds(n))
    r *= d / (bs.to_f * 60.0 / bpm)
  end
  r
end

def __sample_seconds(name)
  name = name.to_s.sub(/\A:/, "")
  $__sdur[name]
end

# sample_duration is how many beats a sample plays for at the current bpm,
# with the same options sample takes.
def sample_duration(name = nil, *rest, **opts)
  opts = rest.first.merge(opts) if rest.first.is_a?(Hash)
  n = name.is_a?(String) && !__sample_path?(name) ? __sample_filter([name] + rest) : name
  d = __sample_seconds(n.to_s) or return 1.0
  bpm = current_bpm
  o = __ctx[:smpdef].merge(opts)
  st, fi = __sample_region(o)
  r = __sample_rate(n, o, bpm).abs
  r = 1.0 if r.zero?
  secs = d * (fi - st).abs / r
  if o.key?(:sustain) && o[:sustain].to_f >= 0
    secs = [secs, (o[:attack].to_f + o[:decay].to_f + o[:sustain].to_f + o[:release].to_f) * 60.0 / bpm].min
  end
  secs * bpm / 60.0
end
def load_sample(*_a) = nil
def load_samples(*_a) = nil
def sample_free(*_a) = nil
def sample_free_all = nil

$__node_id = 0
def __node_id = ($__node_id += 1)

SLIDES = { note_slide: :nsl, amp_slide: :asl, pan_slide: :psl, cutoff_slide: :cusl, rate_slide: :rsl }.freeze

# __slides adds the slide times (in seconds) a sound starts with or a
# control asks for. slide: sets them all.
def __slides(o, ev, bpm)
  SLIDES.each do |k, key|
    v = o.key?(k) ? o[k] : o[:slide]
    ev[key] = v.to_f * 60.0 / bpm if v
  end
end

# control changes a sound that is playing: its note, amp, pan, cutoff or
# (for a sample) rate, gliding over the *_slide time.
def control(node = nil, *rest, **opts)
  opts = rest.first.merge(opts) if rest.first.is_a?(Hash)
  return nil if node.nil?
  return node.to_a.each { |x| control(x, **opts) } && nil if node.is_a?(Array) || node.is_a?(Ring)
  return nil unless node.is_a?(SusynthNode)
  if node.kind == :fx
    # the effect's new settings apply to the sounds that start from now on
    node.opts&.merge!(opts.reject { |k, _| k.to_s.end_with?("_slide") })
    return nil
  end
  th = __need_thread("control")
  bpm = th.ctx[:bpm]
  ev = {}
  if opts.key?(:note)
    n = opts[:note]
    n = n.to_a.first if n.is_a?(Array) || n.is_a?(Ring)
    nn = note(n)
    ev[:n] = (nn + th.ctx[:transpose] + th.ctx[:octave] * 12).to_f if nn
  end
  %i[amp pan cutoff rate].each { |k| ev[k] = opts[k].to_f if opts.key?(k) && opts[k].is_a?(Numeric) }
  __slides(opts, ev, bpm)
  return nil if ev.empty?
  node.ids.each { |id| __emit("c", node.name, ev.merge(id: id)) }
  nil
end

# ---- MIDI -------------------------------------------------------------------
# These go to the MIDI output in Settings, on time. With none set (and when
# rendering to a file) they make no sound and say nothing.

# __midi sends one message to each channel the call names: channel: may be
# a number, a list of them, or "*" for all sixteen (nil o: no channel).
def __midi(delay, o, status, d1 = nil, d2 = nil)
  chans = o.nil? ? [nil] : __midi_chs(o)
  chans.each do |ch|
    ev = { st: ch ? status | ch : status }
    ev[:d1] = d1 if d1
    ev[:d2] = d2 if d2
    __emit("m", "midi", ev, delay)
  end
  nil
end

def __midi_chs(o)
  c = o[:channel] || 1
  return (0..15).to_a if c == "*" || c == :* || c == :all
  Array(c.respond_to?(:to_a) ? c.to_a : c).map { |x| x.to_i.clamp(1, 16) - 1 }.uniq
end
def __midi7(v, d) = (v.nil? ? d : v).to_f.round.clamp(0, 127)

def midi_note_on(n, vel = 127, **o)
  nn = note(n) or return nil
  __midi(0, o, 0x90, nn.round.clamp(0, 127), __midi7(o[:velocity] || vel, 127))
end

def midi_note_off(n, vel = 64, **o)
  nn = note(n) or return nil
  __midi(0, o, 0x80, nn.round.clamp(0, 127), __midi7(vel, 64))
end

# midi plays a note on, then off after sustain: beats.
def midi(n, vel = nil, **o)
  nn = note(n) or return nil
  th = __need_thread("midi")
  nn = nn.round.clamp(0, 127)
  __midi(0, o, 0x90, nn, __midi7(o[:vel] || vel || o[:velocity], 127))
  __midi((o[:sustain] || 1).to_f * 60.0 / th.ctx[:bpm], o, 0x80, nn, 64)
end

def midi_cc(control, value = 0, **o) = __midi(0, o, 0xB0, __midi7(control, 0), __midi7(value, 0))
def midi_program_change(program, **o) = __midi(0, o, 0xC0, __midi7(program, 0))
def midi_channel_pressure(val, **o) = __midi(0, o, 0xD0, __midi7(val, 0))
def midi_poly_pressure(n, val = 0, **o) = (nn = note(n)) && __midi(0, o, 0xA0, nn.round.clamp(0, 127), __midi7(val, 0))

# midi_pitch_bend takes -1..1 (0 is centre) or a 14-bit number
def midi_pitch_bend(val = 0, **o)
  v = val.is_a?(Integer) && val.abs > 1 ? val : ((val.to_f.clamp(-1, 1) + 1) * 8191.5).round
  v = v.clamp(0, 16383)
  __midi(0, o, 0xE0, v & 0x7F, v >> 7)
end

def midi_all_notes_off(**o) = __midi(0, o, 0xB0, 123, 0)
def midi_sound_off(**o) = __midi(0, o, 0xB0, 120, 0)
def midi_clock_tick(**_o) = __midi(0, nil, 0xF8)
def midi_start(**_o) = __midi(0, nil, 0xFA)
def midi_continue(**_o) = __midi(0, nil, 0xFB)
def midi_stop(**_o) = __midi(0, nil, 0xFC)
%i[midi_raw midi_clock_beat midi_reset midi_devices midi_ports].each { |m| define_method(m) { |*_a, **_o| nil } }

%i[osc osc_send].each do |m|
  define_method(m) do |*_a, **_o|
    __warn_once("io#{m}", "#{m} isn't available inside the Ruby sandbox; use Settings > MIDI output or OSC instead")
    nil
  end
end

%i[use_debug use_arg_checks use_timing_guarantees use_real_time use_sched_ahead_time
   set_sched_ahead_time! set_volume! set_mixer_control! use_osc use_midi_defaults set_audio_latency!
   set_cent_tuning! use_tuning use_cent_tuning].each do |m|
  define_method(m) { |*_a, **_o| nil }
end

def use_cue_logging(on = true, &blk)
  return ($__cue_log = !!on) && nil unless blk
  old = $__cue_log
  $__cue_log = !!on
  begin
    blk.call
  ensure
    $__cue_log = old
  end
  nil
end

def set(k, v) = ($__store[k] = v) && nil
def get(k = nil) = k.nil? ? $__store : $__store[k]

def define(name, &blk)
  raise ArgumentError, "define needs a name and a do ... end block" unless blk
  Object.send(:define_method, name.to_sym, &blk)
  Object.send(:private, name.to_sym)
  nil
end

def puts(*a)
  return nil if __silent?($__cur)
  __send("log", a.map(&:to_s).join(" "))
  nil
end

def print(*a) = puts(*a)

def p(*a)
  __send("log", a.map(&:inspect).join(" ")) unless __silent?($__cur)
  a.size <= 1 ? a.first : a
end

# ---- the rest of Sonic Pi's language ---------------------------------------------
#
# Sonic Pi adds a few methods to Ruby's own classes, and has many functions
# that only matter with its own app (MIDI devices, recording, Ableton Link).
# Those run here as harmless no-ops, so code written for Sonic Pi carries on.

class String
  def ring = Ring.new(chars)
  def tick(*a, **o) = ring.tick(*a, **o)
  def look(*a, **o) = ring.look(*a, **o)
end

class Symbol
  def ring = to_s.ring
  def +(o) = rest? ? self : note(self) + (o.is_a?(Symbol) ? note(o) : o)
  def -(o) = rest? ? self : note(self) - (o.is_a?(Symbol) ? note(o) : o)
  def to_f = rest? ? 0.0 : note(self).to_f
  def to_i = to_f.to_i
  def rest? = self == :r || self == :rest
end

class NilClass
  def +(_o) = nil
  def -(_o) = nil
end

class Float
  def times(&blk)
    return to_enum(:times) unless blk
    to_i.times { |i| blk.call(i.to_f) }
    self
  end
end

class Numeric
  # Sonic Pi's max and min bound a number; Ruby has no Numeric#max.
  def max(o) = self <= o ? self : o
  def min(o) = self >= o ? self : o
end

class Array
  def pick(n = nil, *opts)
    n, opts = nil, [n] unless n.nil? || n.is_a?(Numeric)
    skip = opts.first.is_a?(Hash) ? opts.first[:skip].to_i : 0
    skip.times { choose }
    Array.new((n || 1).to_i) { choose }
  end
  # inside running code, shuffle and sample use the thread's own random
  # numbers, so every run of the same code sounds the same
  alias_method :__orig_shuffle, :shuffle
  alias_method :__orig_sample, :sample
  def shuffle(*a, **o) = $__cur && o.empty? ? __orig_shuffle(random: __ctx[:rng]) : __orig_shuffle(*a, **o)
  def sample(*a, **o) = $__cur && a.empty? && o.empty? ? choose : __orig_sample(*a, **o)
end

class Ring
  def ramp = Ramp.new(to_a)
  def pick(n = nil, *opts) = Ring.new(to_a.pick(n, *opts))
  def notes = Ring.new(to_a.map { |x| note(x) })
end

# A ramp is a ring that stops at its last value instead of wrapping round.
class Ramp < Ring
  def [](i, len = nil)
    return super if len || i.is_a?(Range)
    a = to_a
    a.empty? ? nil : a[i.to_i.clamp(0, a.size - 1)]
  end
end

def ramp(*a) = Ramp.new(a)
def vector(*a) = a.freeze
def map(*a, **kw) = (a.first.is_a?(Hash) ? a.first : {}).merge(kw)
def rest?(n) = n.nil? || n == :r || n == :rest
def inc(n) = n + 1
def dec(n) = n - 1
def doubles(start, num) = Ring.new(Array.new(num.to_i) { |i| start * 2**i })
def halves(start, num) = Ring.new(Array.new(num.to_i) { |i| start / 2.0**i })
def octs(start, num_octaves = 1) = Ring.new(Array.new(num_octaves.to_i) { |i| note(start) + 12 * i })
def note_range(low, high, pitches: nil, **_o)
  lo, hi = note(low), note(high)
  lo, hi = hi, lo if lo > hi
  pcs = pitches && pitches.to_a.map { |x| note(x) % 12 }
  Ring.new((lo.ceil..hi.floor).select { |x| pcs.nil? || pcs.include?(x % 12) })
end
def math_scale(v, lo, hi, nlo, nhi) = nlo + (v - lo).to_f * (nhi - nlo) / (hi - lo)
def pitch_to_ratio(m) = 2.0**(m.to_f / 12)
def ratio_to_pitch(r) = 12 * Math.log2(r.to_f)
def rdist(width, centre = 0, **_o) = centre + rrand(-width, width)
def rand_back(_n = 1) = nil
def rand_i_look(m = 2) = Random.new(__ctx[:rng].seed + 1).rand(m.to_i)
def tuplets(list, **_o) = Ring.new(list.to_a)

# time_warp plays its block a little earlier or later (in beats) without
# moving the thread's own time.
def time_warp(times = 0, params = nil, &blk)
  raise ArgumentError, "time_warp needs a do ... end block" unless blk
  th = __need_thread("time_warp")
  had = params
  times = [times] if times.is_a?(Numeric)
  params ||= times
  ps = params.to_a
  base, old = th.time, th.ctx[:warp]
  th.ctx[:warp] = true
  times.to_a.each_with_index do |d, i|
    th.time = base + d.to_f * 60.0 / th.ctx[:bpm] / th.ctx[:density]
    case blk.arity
    when 0 then blk.call
    when 1 then blk.call(ps[i % ps.size])
    else blk.call(d, had ? ps[i % ps.size] : i)
    end
    th.time = base
  end
  nil
ensure
  if th
    th.time = base
    th.ctx[:warp] = old
  end
end

def with_swing(shift = 0.1, pulse = 4, tick: :swing, offset: 0, **_o, &blk)
  raise ArgumentError, "with_swing needs a do ... end block" unless blk
  if ((tick_counter_advance(tick) + offset.to_i) % pulse.to_i).zero?
    time_warp(shift, &blk)
  else
    blk.call
  end
end

def use_bpm_mul(m) = use_bpm(__ctx[:bpm] * m.to_f)
def with_bpm_mul(m, &blk) = with_bpm(__ctx[:bpm] * m.to_f, &blk)
def __sample_bpm(name, beats) = (d = __sample_seconds(name)) ? beats.to_f * 60.0 / d : current_bpm
def use_sample_bpm(name, num_beats: 1, **_o) = use_bpm(__sample_bpm(name, num_beats))
def with_sample_bpm(name, num_beats: 1, **_o, &blk) = with_bpm(__sample_bpm(name, num_beats), &blk)
def with_merged_sample_defaults(**o, &blk) = __with(:smpdef, __ctx[:smpdef].merge(o), &blk)
def current_octave = __ctx[:octave]
def current_synth_defaults = __ctx[:sdef]
def current_sample_defaults = __ctx[:smpdef]
def current_beat_duration = 60.0 / current_bpm
def current_bpm_mode = :normal
def current_random_seed = __ctx[:rng].seed
def current_random_source = :white
def current_sched_ahead_time = 0.5
def current_time = Time.at(0) + ($__cur ? $__cur.time : 0)
def current_debug = false
def current_arg_checks = false
def current_cent_tuning = 0
def current_volume = 1
def current_drive = 0
def current_midi_defaults = {}
def current_track = nil
def version = "5.1.0"
def status = {}
def block_slept? = !!($__cur && $__cur.slept)

def block_duration(&blk)
  th = __need_thread("block_duration")
  t0 = th.time
  blk.call
  (th.time - t0) * th.ctx[:bpm] / 60.0
end

# defonce runs its block the first time only and keeps the answer.
$__defonce = {}
def defonce(name, override: false, &blk)
  key = name.to_sym
  $__defonce[key] = blk.call if override || !$__defonce.key?(key)
  val = $__defonce[key]
  Object.send(:define_method, key) { |*_a| val }
  Object.send(:private, key)
  nil
end
def ndefine(name, &blk) = define(name, &blk)

def comment(*_a) = nil
def uncomment(*_a, &blk) = blk&.call

def use_random_source(_s) = nil
def with_random_source(_s, &blk) = blk&.call

def kill(node = nil, **_o)
  return __kill_named(node) if node.is_a?(Symbol) || node.is_a?(String)
  return nil unless node.is_a?(SusynthNode) && $__cur
  node.ids.each { |id| __emit("c", node.name, { id: id, kill: 1 }) }
  nil
end

def assert(arg, msg = nil) = arg ? true : raise("Assert failed#{msg ? ": #{msg}" : ''}")
def assert_not(arg, msg = nil) = assert(!arg, msg)
def assert_equal(a, b, msg = nil) = assert(a == b, msg || "#{a.inspect} isn't #{b.inspect}")
def assert_not_equal(a, b, msg = nil) = assert(a != b, msg || "#{a.inspect} is #{b.inspect}")
def assert_similar(a, b, msg = nil) = assert(a == b || (a.to_f - b.to_f).abs < 1e-5, msg)
def assert_error(cls = StandardError, msg = nil)
  yield
  raise "Assert failed: expected #{cls}#{msg ? ": #{msg}" : ''}"
rescue cls
  true
end

def run_code(code, **_o) = TOPLEVEL_BINDING.eval(code.to_s, "run_code", 1) && nil

%i[run_file eval_file load_buffer load_example load_synthdef load_synthdefs].each do |m|
  define_method(m) do |*_a, **_o|
    __warn_once("file#{m}", "#{m} can't read other files from inside susynth; it is skipped")
    nil
  end
end

def spark(*a) = puts(a.flatten.inspect)
def spark_graph(*a) = puts(a.flatten.inspect)

def sample_paths(*a) = Ring.new(a.flatten.map(&:to_s))
def sample_groups = Ring.new($__samples.map { |n| n.split("_").first }.uniq.map(&:to_sym))
def sample_loaded?(*_a) = true
def sample_info(*a) = { name: a.first, duration: __sample_seconds(a.first) }
def sample_buffer(*a) = a.first
def buffer(*a, **_o) = a.first

# MIDI, recording, Ableton Link, live audio and tracks need Sonic Pi's own
# app and devices; here they quietly do nothing.
def midi_pc(program, **o) = midi_program_change(program, **o)

%i[midi_local_control_off midi_local_control_on midi_mode midi_sysex
   recording_start recording_stop recording_save recording_delete
   link link_audio link_sync set_link_bpm! live_audio live_track use_track
   track_control track_midi track_midi_all_notes_off track_midi_cc track_midi_note_off
   track_midi_note_on track_midi_pitch_bend].each do |m|
  define_method(m) do |*_a, **_o|
    __warn_once("io#{m}", "#{m} needs Sonic Pi's app or a device; it does nothing in susynth")
    nil
  end
end

def midi_notes(*a) = Ring.new(a.map { |x| note(x) })
def midi_clock_sources = Ring.new([])
def tracks = Ring.new([])
def midi_sync(*_a, **_o) = nil

%i[reset clear reset_mixer! set_control_delta! set_drive! set_recording_bit_depth! use_arg_bpm_scaling
   use_midi_logging use_osc_logging use_merged_midi_defaults scsynth_info].each do |m|
  define_method(m) { |*_a, **_o| nil }
end

# with_* settings that only matter to Sonic Pi's app: the block runs as it is.
%i[with_arg_bpm_scaling with_arg_checks with_cent_tuning with_debug with_merged_midi_defaults
   with_midi_defaults with_midi_logging with_osc with_osc_logging with_real_time with_sched_ahead_time
   with_send with_timing_guarantees with_track with_tuning].each do |m|
  define_method(m) { |*_a, **_o, &blk| blk&.call }
end

def with_cue_logging(on = true, &blk) = use_cue_logging(on, &blk)

# ---- evaluation ---------------------------------------------------------------------

# Sonic Pi rewrites Lisp-style calls such as (ring 1, 2, 3) or
# (chord :e3, :minor) into ring(1, 2, 3) before running the code. The
# rewrite keeps every character's position, so line numbers stay right.
VEC_FNS = %w[bools chord chord_degree chord_invert doubles halves knit line midi_notes note_range octs
             ramp range ring sample_names sample_paths scale spread stretch tracks vector].freeze
STRING_OR_COMMENT = /'(?:\\.|[^'\\])*'|"(?:\\.|[^"\\])*"|#[^\n]*/m

def __preparse(code)
  rb = String.new(code)
  masked = rb.gsub(STRING_OR_COMMENT) { |m| m.gsub(/[^\n]/, " ") }
  VEC_FNS.each do |fn|
    re = /\((\s*)#{fn}([,[:space:]]+)/
    pos = 0
    while (m = masked.match(re, pos))
      rep = " " + m[1] + fn + "(" + (" " * (m[2].size - 1))
      rb[m.begin(0)...m.end(0)] = rep
      masked[m.begin(0)...m.end(0)] = rep
      pos = m.end(0)
    end
  end
  rb
end

def __syntax_line(e)
  m = e.message.match(/buffer:(\d+)/)
  m ? m[1].to_i : 0
end

def __syntax_msg(e)
  msgs = e.message.lines.filter_map { |l| (m = l.match(/\^[~^]*\s+(\S.*)$/)) && m[1].strip }
  return "a do, if or def is missing its end" if msgs.any? { |m| m =~ /expected an? `end'|expected an? `end`|end-of-input/ }
  msgs.find { |m| m.include?("expected") } || msgs.first ||
    e.message.lines.first.to_s.sub(/\Abuffer:\d+:\s*/, "").strip
end

# __parse checks the whole buffer with Ruby's compiler (for good messages with
# line numbers), then returns its syntax tree.
def __parse(code)
  RubyVM::InstructionSequence.compile(code, "buffer", "buffer", 1)
  RubyVM::AbstractSyntaxTree.parse(code)
end

# first/last are 1-based inclusive lines chosen by the editor (so the highlight
# and the code that runs can't disagree). first <= 0 runs the whole buffer and
# resets top-level defaults, the way Sonic Pi's Run does.
def __run(id, first, last, at, swap, code)
  code = __preparse(code)
  begin
    __parse(code)
  rescue SyntaxError => e
    __send("err", __syntax_line(e), "", __syntax_msg(e))
    __send("ran", id, 0, 0)
    return
  end
  lines = code.lines
  full = first <= 0
  if full
    first, last = 1, lines.size
    $__main_ctx = __default_ctx
  end
  if last < first || lines.empty? || first > lines.size
    __send("ran", id, 0, 0)
    return
  end
  last = lines.size if last > lines.size
  slice = __arm_spins(lines[(first - 1)..(last - 1)].join)
  $__swap = swap
  th = __spawn(:run, :main, nil, at) do
    __send("bpm", 60.0, format("%.6f", at)) if full # until the code's own use_bpm
    TOPLEVEL_BINDING.eval(slice, "buffer", first)
  end
  th.run_id = id.to_i
  __send("ran", id, first, last)
end

def __check(code)
  __parse(__preparse(code))
rescue SyntaxError => e
  __send("err", __syntax_line(e), "", __syntax_msg(e))
end

def __main
  while (line = $stdin.gets)
    cmd, *args = line.chomp.split("\t", -1)
    case cmd
    when "run"
      code = $stdin.read(args[5].to_i).to_s.force_encoding("UTF-8")
      __run(args[0], args[1].to_i, args[2].to_i, args[3].to_f, args[4] != "pass", code)
    when "chk"
      __check($stdin.read(args[0].to_i).to_s.force_encoding("UTF-8"))
    when "adv"
      __advance(args[0].to_f)
    when "stop"
      __stop_all
    when "deck"
      __new_deck
    when "stopold"
      __stop_old
    when "kill"
      __kill_named(args[0])
    when "syn"
      $__synths = args[0].to_s.split(",")
    when "smp"
      # name or name=seconds (its length at rate 1)
      $__sdur = {}
      $__samples = args[0].to_s.split(",").map do |e|
        n, d = e.split("=", 2)
        $__sdur[n] = d.to_f if d
        n
      end
    when "ping"
      __send("pong", RUBY_VERSION)
    end
    __flush
  end
end

__main
