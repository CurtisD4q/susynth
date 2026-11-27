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
  __send("log", msg)
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
  b = beats.to_f
  raise ArgumentError, "sleep needs a number of beats, 0 or more" if b.negative? || b.nan?
  th.time += b * 60.0 / th.ctx[:bpm] / th.ctx[:density]
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

def stop
  raise SusynthStop
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
  c = __ctx
  old = c[:density]
  c[:density] = old * d.to_f
  d.to_i.times { blk.call }
ensure
  c[:density] = old if c
end

def use_bpm(b, &blk)
  raise ArgumentError, "use_bpm does not take a block, use with_bpm" if blk
  b = b.to_f
  raise ArgumentError, "bpm must be above 0" unless b.positive?
  __top_set(:bpm, b)
  __send("bpm", b, format("%.6f", $__cur ? $__cur.time : 0)) if $__cur.nil? || $__cur.kind == :run
  nil
end

def with_bpm(b, &blk)
  c = __ctx
  old = c[:bpm]
  c[:bpm] = b.to_f
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

CHORDS = {
  "1" => [0], "5" => [0, 7], "+5" => [0, 4, 8], "m+5" => [0, 3, 8], "sus2" => [0, 2, 7], "sus4" => [0, 5, 7],
  "6" => [0, 4, 7, 9], "m6" => [0, 3, 7, 9], "7sus2" => [0, 2, 7, 10], "7sus4" => [0, 5, 7, 10],
  "7-5" => [0, 4, 6, 10], "m7-5" => [0, 3, 6, 10], "7+5" => [0, 4, 8, 10], "m7+5" => [0, 3, 8, 10],
  "9" => [0, 4, 7, 10, 14], "m9" => [0, 3, 7, 10, 14], "m7+9" => [0, 3, 7, 10, 14], "maj9" => [0, 4, 7, 11, 14],
  "9sus4" => [0, 5, 7, 10, 14], "6*9" => [0, 4, 7, 9, 14], "m6*9" => [0, 3, 9, 7, 14], "7-9" => [0, 4, 7, 10, 13],
  "m7-9" => [0, 3, 7, 10, 13], "7-10" => [0, 4, 7, 10, 15], "9+5" => [0, 10, 13], "m9+5" => [0, 10, 14],
  "7+5-9" => [0, 4, 8, 10, 13], "m7+5-9" => [0, 3, 8, 10, 13], "11" => [0, 4, 7, 10, 14, 17],
  "m11" => [0, 3, 7, 10, 14, 17], "maj11" => [0, 4, 7, 11, 14, 17], "11+" => [0, 4, 7, 10, 14, 18],
  "m11+" => [0, 3, 7, 10, 14, 18], "13" => [0, 4, 7, 10, 14, 17, 21], "m13" => [0, 3, 7, 10, 14, 17, 21],
  "add2" => [0, 2, 4, 7], "add4" => [0, 4, 5, 7], "add9" => [0, 4, 7, 14], "add11" => [0, 4, 7, 17],
  "add13" => [0, 4, 7, 21], "madd2" => [0, 2, 3, 7], "madd4" => [0, 3, 5, 7], "madd9" => [0, 3, 7, 14],
  "madd11" => [0, 3, 7, 17], "madd13" => [0, 3, 7, 21], "major" => [0, 4, 7], "M" => [0, 4, 7],
  "minor" => [0, 3, 7], "m" => [0, 3, 7], "major7" => [0, 4, 7, 11], "dom7" => [0, 4, 7, 10], "7" => [0, 4, 7, 10],
  "M7" => [0, 4, 7, 11], "minor7" => [0, 3, 7, 10], "m7" => [0, 3, 7, 10], "augmented" => [0, 4, 8],
  "a" => [0, 4, 8], "diminished" => [0, 3, 6], "dim" => [0, 3, 6], "i" => [0, 3, 6], "diminished7" => [0, 3, 6, 9],
  "dim7" => [0, 3, 6, 9], "i7" => [0, 3, 6, 9], "halfdim" => [0, 3, 6, 10], "m7b5" => [0, 3, 6, 10],
  "power" => [0, 7], "sus" => [0, 5, 7], "mmaj7" => [0, 3, 7, 11], "m7b9" => [0, 3, 7, 10, 13]
}.freeze

SCALES = {
  diatonic: [2, 2, 1, 2, 2, 2, 1], ionian: [2, 2, 1, 2, 2, 2, 1], major: [2, 2, 1, 2, 2, 2, 1],
  dorian: [2, 1, 2, 2, 2, 1, 2], phrygian: [1, 2, 2, 2, 1, 2, 2], lydian: [2, 2, 2, 1, 2, 2, 1],
  mixolydian: [2, 2, 1, 2, 2, 1, 2], aeolian: [2, 1, 2, 2, 1, 2, 2], minor: [2, 1, 2, 2, 1, 2, 2],
  natural_minor: [2, 1, 2, 2, 1, 2, 2], locrian: [1, 2, 2, 1, 2, 2, 2],
  hex_major6: [2, 2, 1, 2, 2, 3], hex_dorian: [2, 1, 2, 2, 3, 2], hex_phrygian: [1, 2, 2, 3, 2, 2],
  hex_major7: [2, 2, 3, 2, 2, 1], hex_sus: [2, 3, 2, 2, 1, 2], hex_aeolian: [3, 2, 2, 1, 2, 2],
  major_pentatonic: [2, 2, 3, 2, 3], minor_pentatonic: [3, 2, 2, 3, 2], yu: [3, 2, 3, 2, 2],
  zhi: [2, 3, 2, 2, 3], shang: [2, 3, 2, 3, 2], jiao: [3, 2, 3, 2, 2], gong: [2, 2, 3, 2, 3],
  ritusen: [2, 3, 2, 2, 3], egyptian: [2, 3, 2, 3, 2], kumai: [2, 1, 4, 2, 3], hirajoshi: [2, 1, 4, 1, 4],
  iwato: [1, 4, 1, 4, 2], chinese: [4, 2, 1, 4, 1], indian: [4, 1, 2, 3, 2], pelog: [1, 2, 4, 1, 4],
  prometheus: [2, 2, 2, 5, 1], scriabin: [1, 3, 3, 2, 3], gong_scale: [2, 2, 3, 2, 3],
  harmonic_minor: [2, 1, 2, 2, 1, 3, 1], harmonic_major: [2, 2, 1, 2, 1, 3, 1],
  melodic_minor: [2, 1, 2, 2, 2, 2, 1], melodic_minor_asc: [2, 1, 2, 2, 2, 2, 1],
  melodic_minor_desc: [2, 1, 2, 2, 1, 2, 2], melodic_major: [2, 2, 1, 2, 1, 2, 2],
  super_locrian: [1, 2, 1, 2, 2, 2, 2], hungarian_minor: [2, 1, 3, 1, 1, 3, 1],
  romanian_minor: [2, 1, 3, 1, 2, 1, 2], neapolitan_major: [1, 2, 2, 2, 2, 2, 1],
  neapolitan_minor: [1, 2, 2, 2, 1, 3, 1], enigmatic: [1, 3, 2, 2, 2, 1, 1], spanish: [1, 3, 1, 2, 1, 2, 2],
  bartok: [2, 2, 1, 2, 1, 2, 2], blues_major: [2, 1, 1, 3, 2, 3], blues_minor: [3, 2, 1, 1, 3, 2],
  whole_tone: [2, 2, 2, 2, 2, 2], whole: [2, 2, 2, 2, 2, 2], chromatic: [1] * 12,
  diminished: [1, 2, 1, 2, 1, 2, 1, 2], diminished2: [2, 1, 2, 1, 2, 1, 2, 1],
  augmented: [3, 1, 3, 1, 3, 1], augmented2: [1, 3, 1, 3, 1, 3], leading_whole: [2, 2, 2, 2, 2, 1, 1],
  purvi: [1, 3, 2, 1, 1, 3, 1], todi: [1, 2, 3, 1, 1, 3, 1], marva: [1, 3, 2, 1, 2, 2, 1],
  bhairav: [1, 3, 1, 2, 1, 3, 1], ahirbhairav: [1, 3, 1, 2, 2, 1, 2]
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
  def shuffle = Ring.new(@a.shuffle(random: __ctx[:rng]))
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
  def +(o) = Ring.new(@a + o.to_a)
  def -(o) = Ring.new(@a - o.to_a)
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

def range(s, e, step_or_opts = 1, step: nil, inclusive: false)
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
def rrand_i(a, b, **_o) = a.to_i + __ctx[:rng].rand((b.to_i - a.to_i).abs + 1) * (b < a ? -1 : 1)
def rand(m = 1) = __ctx[:rng].rand * m
def rand_i(m = 2) = __ctx[:rng].rand(m.to_i)
def rand_look(m = 1) = Random.new(__ctx[:rng].seed + 1).rand * m
def dice(n = 6) = __ctx[:rng].rand(n.to_i) + 1
def one_in(n) = __ctx[:rng].rand(n.to_i).zero?
def choose(list) = list.to_a.empty? ? nil : list.to_a[__ctx[:rng].rand(list.to_a.size)]
def pick(list, n = 1, **_o) = Ring.new(Array.new(n) { choose(list) })
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

FX_SUPPORTED = %i[reverb gverb echo ping_pong lpf rlpf nlpf nrlpf hpf rhpf nhpf nrhpf bitcrusher krush
                  distortion tanh level pan compressor normaliser].freeze

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
  __check_synth(s)
  __top_set(:synth, s.to_sym)
end

def with_synth(s, &blk)
  __check_synth(s)
  __with(:synth, s.to_sym, &blk)
end

def current_synth = __ctx[:synth]
def synth_names = $__synths.map(&:to_sym)
def sample_names(*_a) = Ring.new($__samples.map(&:to_sym))
def all_sample_names = sample_names
def fx_names = FX_SUPPORTED

def __check_synth(s)
  return if $__synths.empty? || $__synths.include?(s.to_s)
  raise ArgumentError, "synth #{s.inspect} isn't available (try :beep :saw :prophet :tb303 :pluck :fm …)"
end

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
  name = name.to_sym
  unless FX_SUPPORTED.include?(name)
    __warn_once("fx#{name}", "with_fx #{name.inspect} isn't available in susynth; the block plays without it")
    r = nil
    reps.to_i.times { r = blk.call(nil) }
    return r
  end
  c = __ctx
  c[:fx] = c[:fx] + [[name, opts]]
  r = nil
  reps.to_i.times { r = blk.call(nil) }
  r
ensure
  c[:fx] = c[:fx][0...-1] if c && FX_SUPPORTED.include?(name)
end

def __beats(o, k, bpm) = o.key?(k) ? o[k].to_f * 60.0 / bpm : nil

# __fx folds the with_fx stack into per-note settings for the Go side.
def __fx(out, bpm)
  __ctx[:fx].each do |name, f|
    case name
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
    when :lpf, :rlpf, :nlpf, :nrlpf
      c = (f[:cutoff] || 100).to_f
      out[:flpf] = out[:flpf] ? [out[:flpf], c].min : c
    when :hpf, :rhpf, :nhpf, :nrhpf
      c = (f[:cutoff] || 100).to_f
      out[:fhpf] = out[:fhpf] ? [out[:fhpf], c].max : c
    when :bitcrusher
      b = (f[:bits] || 8).to_f
      out[:crush] = out[:crush] ? [out[:crush], b].min : b
    when :krush, :distortion, :tanh
      d = (f[:distort] || f[:gain] && (f[:gain].to_f / 10) || 0.5).to_f.clamp(0, 1)
      out[:shape] = [out[:shape].to_f, d].max
    when :level
      out[:amp] = out[:amp].to_f * (f[:amp] || 1).to_f
    when :pan
      out[:pan] = (f[:pan] || 0).to_f
    end
  end
  out
end

def __emit(kind, name, o)
  th = __need_thread(kind == "s" ? "sample" : "play")
  line = __buffer_line(caller_locations(2, 12))
  kv = o.map { |k, v| "#{k}=#{v.is_a?(Float) ? v.round(5) : v}" }.join(",")
  return if __silent?(th)
  __send("ev", format("%.6f", th.time), __tname(th), line, kind, name, kv, th.run_id.to_i)
end

def play(n, *rest, **opts)
  opts = rest.first.merge(opts) if rest.first.is_a?(Hash)
  if n.is_a?(Array) || n.is_a?(Ring)
    n.to_a.each { |x| play(x, **opts) }
    return nil
  end
  c = __ctx
  o = c[:sdef].merge(opts)
  return nil if o.key?(:on) && !o[:on]
  s = (o.delete(:synth) || c[:synth]).to_sym
  __check_synth(s)
  nn = note(o.key?(:note) ? o.delete(:note) : n)
  return nil if nn.nil?
  nn += c[:transpose] + c[:octave] * 12
  bpm = c[:bpm]
  ev = { n: nn.to_f, amp: (o[:amp] || 1).to_f, pan: (o[:pan] || 0).to_f }
  %i[attack decay sustain release].each { |k| (v = __beats(o, k, bpm)) && ev[k.to_s[0].to_sym] = v }
  ev[:r] ||= 60.0 / bpm unless o.key?(:release)
  ev[:cutoff] = o[:cutoff].to_f if o[:cutoff]
  __fx(ev, bpm)
  __emit("p", s, ev)
  nil
end

def synth(name, **opts)
  play(opts.key?(:note) ? opts[:note] : 52, synth: name, **opts.except(:note))
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

def sample(name, *rest, **opts)
  idx = rest.first.is_a?(Integer) ? rest.first : nil
  c = __ctx
  o = c[:smpdef].merge(opts)
  return nil if o.key?(:on) && !o[:on]
  case name
  when Symbol
    n = name.to_s
    unless $__samples.empty? || $__samples.include?(n)
      raise ArgumentError, "sample #{name.inspect} isn't available here; drums like :bd_haus :drum_snare_hard " \
                           ":drum_cymbal_closed are built in, or put #{n}.wav in your samples folder"
    end
  when String then n = name
  else raise ArgumentError, "sample needs a name like :bd_haus or a path to a .wav file"
  end
  bpm = c[:bpm]
  ev = { amp: (o[:amp] || 1).to_f, pan: (o[:pan] || 0).to_f, rate: (o[:rate] || 1).to_f }
  ev[:i] = idx if idx
  ev[:cutoff] = o[:cutoff].to_f if o[:cutoff]
  %i[attack release].each { |k| (v = __beats(o, k, bpm)) && ev[k.to_s[0].to_sym] = v }
  if o[:beat_stretch] || o[:start] || o[:finish] || o[:pitch]
    __warn_once("smpopt", "sample options beat_stretch/start/finish/pitch are ignored in susynth")
  end
  __fx(ev, bpm)
  __emit("s", n, ev)
  nil
end

def sample_duration(*_a, **_o) = 1.0 * 60.0 / current_bpm
def load_sample(*_a) = nil
def load_samples(*_a) = nil
def sample_free(*_a) = nil
def sample_free_all = nil

def control(*_a, **_o)
  __warn_once("control", "control isn't supported in susynth: notes can't be changed once started")
  nil
end

%i[midi midi_note_on midi_note_off midi_cc midi_pitch_bend osc osc_send].each do |m|
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

# ---- evaluation ---------------------------------------------------------------------

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
  __parse(code)
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
      $__samples = args[0].to_s.split(",")
    when "ping"
      __send("pong", RUBY_VERSION)
    end
    __flush
  end
end

__main
