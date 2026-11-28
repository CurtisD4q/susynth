# Ruby's own libraries that Sonic Pi code asks for with require (or reaches
# through autoload, as Set). The Ruby inside susynth has none of them on
# disk, so they are written here, small, and loaded the first time one is
# required. Anything else is still a LoadError, as on a machine without that
# gem.

$__stdlib = {}
$__stdlib_done = {}

# Libraries that need nothing: their classes are built in, or they are only
# there for what the file will do with them.
$__stdlib_noop = %w[thread rubygems English pp prettyprint time date bigdecimal bigdecimal/util digest
                    digest/md5 digest/sha1 digest/sha2 fileutils tempfile tmpdir pathname uri open-uri
                    optparse logger io/console readline etc zlib base64 shellwords erb].freeze

$__stdlib["set"] = <<~'RUBY'
  # assigned first: `class Set` inside the autoload of Set cannot find it
  Set = Class.new unless Object.const_defined?(:Set) && !Object.autoload?(:Set)
  class Set
    include Enumerable
    def self.[](*a) = new(a)
    def initialize(enum = nil, &blk)
      @h = {}
      return unless enum
      raise ArgumentError, "value must be enumerable" unless enum.respond_to?(:each)
      enum.each { |x| add(blk ? blk.call(x) : x) }
    end
    def initialize_copy(o)
      super
      @h = o.instance_variable_get(:@h).dup
    end
    def add(x) = (@h[x] = true; self)
    alias << add
    def add?(x) = @h.key?(x) ? nil : add(x)
    def delete(x) = (@h.delete(x); self)
    def delete?(x) = @h.key?(x) ? delete(x) : nil
    def include?(x) = @h.key?(x)
    alias member? include?
    alias === include?
    alias contain? include?
    def size = @h.size
    alias length size
    def count(*a, &b) = a.empty? && !b ? size : super
    def empty? = @h.empty?
    def clear = (@h.clear; self)
    def each(&b)
      return to_enum(:each) { size } unless b
      @h.each_key(&b)
      self
    end
    def to_a = @h.keys
    def to_set = self
    def first(*a) = a.empty? ? @h.each_key { |k| return k } && nil : to_a.first(*a)
    def merge(*es) = (es.each { |e| e.each { |x| add(x) } }; self)
    def |(o) = dup.merge(o)
    alias union |
    alias + |
    def &(o) = self.class.new(o.each_entry.select { |x| include?(x) })
    alias intersection &
    def -(o) = dup.subtract(o)
    alias difference -
    def subtract(o) = (o.each { |x| delete(x) }; self)
    def ^(o) = (n = self.class.new(o); each { |x| n.add?(x) || n.delete(x) }; n)
    def subset?(o) = size <= o.size && all? { |x| o.include?(x) }
    alias <= subset?
    def superset?(o) = o.size <= size && o.all? { |x| include?(x) }
    alias >= superset?
    def proper_subset?(o) = size < o.size && subset?(o)
    alias < proper_subset?
    def proper_superset?(o) = o.size < size && superset?(o)
    alias > proper_superset?
    def intersect?(o) = any? { |x| o.include?(x) }
    def disjoint?(o) = !intersect?(o)
    def <=>(o) = o.is_a?(Set) ? (self == o ? 0 : subset?(o) ? -1 : superset?(o) ? 1 : nil) : nil
    def ==(o) = o.is_a?(Set) && size == o.size && o.all? { |x| include?(x) }
    def eql?(o) = o.is_a?(Set) && self == o
    def hash = to_a.map(&:hash).sort.hash
    def map!(&b) = (a = to_a.map(&b); clear; merge(a))
    alias collect! map!
    def select!(&b) = (n = size; keep_if(&b); n == size ? nil : self)
    alias filter! select!
    def reject!(&b) = (n = size; delete_if(&b); n == size ? nil : self)
    def keep_if = (to_a.each { |x| delete(x) unless yield(x) }; self)
    def delete_if = (to_a.each { |x| delete(x) if yield(x) }; self)
    def partition(&b) = to_a.partition(&b)
    def group_by(&b) = to_a.group_by(&b)
    def freeze = (@h.freeze; super)
    def inspect = "#<Set: {#{to_a.map(&:inspect).join(', ')}}>"
    alias to_s inspect
  end
  module Enumerable
    def to_set(&b) = Set.new(self, &b)
  end
  class Array
    def to_set(&b) = Set.new(self, &b)
  end
RUBY

$__stdlib["json"] = <<~'RUBY'
  module JSON
    class JSONError < StandardError; end
    class ParserError < JSONError; end
    class GeneratorError < JSONError; end
    def self.generate(o, *_a) = __gen(o, nil, "")
    def self.dump(o, *_a) = generate(o)
    def self.pretty_generate(o, *_a) = __gen(o, "  ", "")
    def self.__gen(o, ind, cur)
      nl = ind ? "\n" : ""
      case o
      when Hash
        return "{}" if o.empty?
        inner = ind ? cur + ind : ""
        "{" + nl + o.map { |k, v| inner + __gen(k.to_s, nil, "") + (ind ? ": " : ":") + __gen(v, ind, inner) }.join("," + nl) + nl + (ind ? cur : "") + "}"
      when Array
        return "[]" if o.empty?
        inner = ind ? cur + ind : ""
        "[" + nl + o.map { |v| inner + __gen(v, ind, inner) }.join("," + nl) + nl + (ind ? cur : "") + "]"
      when String then __str(o)
      when Symbol then __str(o.to_s)
      when nil then "null"
      when true, false then o.to_s
      when Integer then o.to_s
      when Float
        raise GeneratorError, "#{o} not allowed in JSON" if o.nan? || o.infinite?
        o.to_s
      else o.respond_to?(:to_h) ? __gen(o.to_h, ind, cur) : __str(o.to_s)
      end
    end
    def self.__str(s)
      '"' + s.gsub(/["\\\x00-\x1f]/) { |c|
        { '"' => '\\"', "\\" => "\\\\", "\n" => "\\n", "\r" => "\\r", "\t" => "\\t", "\b" => "\\b", "\f" => "\\f" }[c] || format("\\u%04x", c.ord)
      } + '"'
    end
    def self.parse(src, symbolize_names: false, **_o)
      @s = src.to_s
      @i = 0
      @sym = symbolize_names
      v = __val
      __ws
      raise ParserError, "unexpected token at '#{@s[@i, 20]}'" if @i < @s.size
      v
    end
    def self.load(src, *_a) = src.nil? || src.to_s.strip.empty? ? nil : parse(src)
    def self.__ws = (@i += 1 while @s[@i] && " \t\r\n".include?(@s[@i]))
    def self.__val
      __ws
      c = @s[@i]
      case c
      when "{"
        @i += 1
        h = {}
        __ws
        if @s[@i] == "}"
          @i += 1
          return h
        end
        loop do
          __ws
          raise ParserError, "expected a string key at '#{@s[@i, 20]}'" unless @s[@i] == '"'
          k = __string
          __ws
          raise ParserError, "expected ':' at '#{@s[@i, 20]}'" unless @s[@i] == ":"
          @i += 1
          h[@sym ? k.to_sym : k] = __val
          __ws
          if @s[@i] == ","
            @i += 1
          elsif @s[@i] == "}"
            @i += 1
            return h
          else
            raise ParserError, "unexpected token at '#{@s[@i, 20]}'"
          end
        end
      when "["
        @i += 1
        a = []
        __ws
        if @s[@i] == "]"
          @i += 1
          return a
        end
        loop do
          a << __val
          __ws
          if @s[@i] == ","
            @i += 1
          elsif @s[@i] == "]"
            @i += 1
            return a
          else
            raise ParserError, "unexpected token at '#{@s[@i, 20]}'"
          end
        end
      when '"' then __string
      else
        if @s[@i, 4] == "true" then @i += 4; true
        elsif @s[@i, 5] == "false" then @i += 5; false
        elsif @s[@i, 4] == "null" then @i += 4; nil
        elsif (m = /\A-?\d+(\.\d+)?([eE][-+]?\d+)?/.match(@s[@i..]))
          @i += m[0].size
          m[1] || m[2] ? m[0].to_f : m[0].to_i
        else raise ParserError, "unexpected token at '#{@s[@i, 20]}'"
        end
      end
    end
    def self.__string
      @i += 1
      out = +""
      while (c = @s[@i])
        @i += 1
        return out if c == '"'
        if c == "\\"
          e = @s[@i]
          @i += 1
          case e
          when "n" then out << "\n"
          when "t" then out << "\t"
          when "r" then out << "\r"
          when "b" then out << "\b"
          when "f" then out << "\f"
          when "u"
            out << @s[@i, 4].hex.chr("UTF-8")
            @i += 4
          else out << e.to_s
          end
        else
          out << c
        end
      end
      raise ParserError, "unterminated string"
    end
  end
  class Object
    def to_json(*_a) = JSON.generate(self)
  end
  module Kernel
    def JSON(o, *_a) = o.is_a?(String) ? JSON.parse(o) : JSON.generate(o)
  end
RUBY

$__stdlib["stringio"] = <<~'RUBY'
  class StringIO
    include Enumerable
    attr_accessor :string, :pos
    def initialize(s = +"", _mode = nil) = (@string = s; @pos = 0)
    def self.open(*a) = (io = new(*a); block_given? ? yield(io) : io)
    def write(*a) = (a.each { |x| s = x.to_s; @string << s }; a.sum { |x| x.to_s.size })
    def <<(x) = (write(x); self)
    def print(*a) = (write(*a); nil)
    def puts(*a)
      a = [""] if a.empty?
      a.flatten.each { |x| s = x.to_s; write(s.end_with?("\n") ? s : s + "\n") }
      nil
    end
    def printf(f, *a) = (write(format(f, *a)); nil)
    def read(n = nil)
      r = n ? @string[@pos, n] : @string[@pos..].to_s
      @pos += r.to_s.size
      n && r.to_s.empty? ? nil : r.to_s
    end
    def gets(*_a)
      return nil if eof?
      i = @string.index("\n", @pos)
      r = i ? @string[@pos..i] : @string[@pos..]
      @pos += r.size
      r
    end
    def getc = eof? ? nil : (c = @string[@pos]; @pos += 1; c)
    def each_line
      return to_enum(:each_line) unless block_given?
      while (l = gets)
        yield l
      end
    end
    alias each each_line
    def readlines = each_line.to_a
    def rewind = (@pos = 0; 0)
    def eof? = @pos >= @string.size
    def close = nil
    def closed? = false
    def flush = self
    def sync = true
    def sync=(_v); end
    def size = @string.size
    alias length size
    def truncate(n) = (@string.slice!(n..); 0)
  end
RUBY

$__stdlib["csv"] = <<~'RUBY'
  class CSV
    class Row
      include Enumerable
      attr_reader :headers, :fields
      def initialize(h, f) = (@headers = h; @fields = f)
      def [](k) = k.is_a?(Integer) ? @fields[k] : (i = @headers.index(k)) && @fields[i]
      def to_h = @headers.zip(@fields).to_h
      alias to_hash to_h
      def each(&b) = @headers.zip(@fields).each(&b)
      def to_a = @headers.zip(@fields)
      def fetch(k, *d) = (i = @headers.index(k)) ? @fields[i] : (d.empty? ? raise(KeyError, "key not found: #{k}") : d.first)
    end
    class Table
      include Enumerable
      attr_reader :headers
      def initialize(h, rows) = (@headers = h; @rows = rows)
      def each(&b) = @rows.each(&b)
      def [](i) = i.is_a?(Integer) ? @rows[i] : @rows.map { |r| r[i] }
      def size = @rows.size
      alias length size
      def to_a = [@headers] + @rows.map(&:fields)
      def by_col = self
    end
    def self.__parse(str, sep)
      rows = []
      row = []
      field = +""
      q = false
      i = 0
      s = str.to_s
      any = false
      while i < s.size
        c = s[i]
        if q
          if c == '"'
            if s[i + 1] == '"'
              field << '"'
              i += 1
            else
              q = false
            end
          else
            field << c
          end
        elsif c == '"'
          q = true
          any = true
        elsif s[i, sep.size] == sep
          row << field
          field = +""
          any = true
          i += sep.size - 1
        elsif c == "\n" || c == "\r"
          i += 1 if c == "\r" && s[i + 1] == "\n"
          row << field
          rows << row if any || !(row.size == 1 && row[0].empty?)
          row = []
          field = +""
          any = false
        else
          field << c
          any = true
        end
        i += 1
      end
      if any || !field.empty? || !row.empty?
        row << field
        rows << row
      end
      rows
    end
    def self.parse(str, headers: false, col_sep: ",", converters: nil, header_converters: nil, **_o)
      rows = __parse(str, col_sep)
      conv = ->(v) { v.nil? ? v : (converters ? (v =~ /\A-?\d+\z/ ? v.to_i : v =~ /\A-?\d*\.\d+\z/ ? v.to_f : v) : v) }
      rows = rows.map { |r| r.map(&conv) }
      return rows unless headers
      hs = headers == true || headers == :first_row ? rows.shift : headers
      hs = hs.map(&:to_sym) if header_converters == :symbol
      Table.new(hs, rows.map { |r| Row.new(hs, r) })
    end
    def self.parse_line(line, **o) = parse(line, **o).first
    def self.generate_line(row, col_sep: ",", **_o)
      row.map { |v|
        s = v.to_s
        s =~ /["\n\r]/ || s.include?(col_sep) ? '"' + s.gsub('"', '""') + '"' : s
      }.join(col_sep) + "\n"
    end
    def self.generate(str = +"", **o)
      csv = new(str, **o)
      yield csv
      csv.string
    end
    def self.read(path, **o) = parse(File.read(path), **o)
    def self.foreach(path, **o, &b) = parse(File.read(path), **o).each(&b)
    def self.table(path, **o) = read(path, headers: true, **o)
    attr_reader :string
    def initialize(str = +"", col_sep: ",", **_o) = (@string = str; @sep = col_sep)
    def <<(row) = (@string << CSV.generate_line(row, col_sep: @sep); self)
    alias add_row <<
    alias puts <<
  end
RUBY

$__stdlib["securerandom"] = <<~'RUBY'
  module SecureRandom
    def self.hex(n = 16) = Array.new(n) { format("%02x", rand(256)) }.join
    def self.uuid
      h = hex(16)
      "#{h[0, 8]}-#{h[8, 4]}-4#{h[13, 3]}-#{"89ab"[rand(4)]}#{h[17, 3]}-#{h[20, 12]}"
    end
    def self.random_number(n = 0) = n.is_a?(Range) ? rand(n) : (n.to_f > 0 ? rand(n) : rand)
    def self.rand(*a) = Kernel.rand(*a)
    def self.random_bytes(n = 16) = Array.new(n) { Kernel.rand(256).chr }.join
    def self.alphanumeric(n = 16) = Array.new(n) { [*"a".."z", *"A".."Z", *"0".."9"].sample }.join
    def self.base64(n = 16) = alphanumeric((n * 4 / 3.0).ceil)
    def self.urlsafe_base64(n = 16) = base64(n)
  end
RUBY

$__stdlib["ostruct"] = <<~'RUBY'
  class OpenStruct
    def initialize(h = nil) = (@t = {}; h&.each { |k, v| @t[k.to_sym] = v })
    def [](k) = @t[k.to_sym]
    def []=(k, v)
      @t[k.to_sym] = v
    end
    def to_h = @t.dup
    def each_pair(&b) = @t.each_pair(&b)
    def respond_to_missing?(n, p = false) = @t.key?(n.to_s.chomp("=").to_sym) || super
    def method_missing(n, *a)
      s = n.to_s
      if s.end_with?("=") then @t[s.chomp("=").to_sym] = a.first
      else @t[n]
      end
    end
    def dig(*k) = @t.dig(*k.map(&:to_sym))
    def delete_field(k) = @t.delete(k.to_sym)
    def ==(o) = o.is_a?(OpenStruct) && to_h == o.to_h
    def inspect = "#<OpenStruct #{@t.map { |k, v| "#{k}=#{v.inspect}" }.join(', ')}>"
    alias to_s inspect
  end
RUBY

$__stdlib["forwardable"] = <<~'RUBY'
  module Forwardable
    def def_instance_delegator(accessor, method, ali = method)
      acc = accessor.to_s
      define_method(ali) do |*a, **k, &b|
        t = acc.start_with?("@") ? instance_variable_get(acc) : send(acc)
        t.send(method, *a, **k, &b)
      end
    end
    alias def_delegator def_instance_delegator
    def def_instance_delegators(accessor, *methods) = methods.each { |m| def_instance_delegator(accessor, m) }
    alias def_delegators def_instance_delegators
    def instance_delegate(h) = h.each { |ms, acc| Array(ms).each { |m| def_instance_delegator(acc, m) } }
    alias delegate instance_delegate
  end
RUBY

$__stdlib["singleton"] = <<~'RUBY'
  module Singleton
    def self.included(base)
      base.extend(ClassMethods)
      base.private_class_method :new, :allocate
    end
    module ClassMethods
      def instance = (@__instance ||= new)
    end
    def clone = raise(TypeError, "can't clone instance of singleton #{self.class}")
    def dup = raise(TypeError, "can't dup instance of singleton #{self.class}")
  end
RUBY

$__stdlib["observer"] = <<~'RUBY'
  module Observable
    def add_observer(o, func = :update) = ((@observer_peers ||= {})[o] = func)
    def delete_observer(o) = (@observer_peers ||= {}).delete(o)
    def delete_observers = (@observer_peers ||= {}).clear
    def count_observers = (@observer_peers ||= {}).size
    def changed(s = true) = (@observer_state = s)
    def changed? = !!@observer_state
    def notify_observers(*a)
      return unless @observer_state
      (@observer_peers ||= {}).each { |o, f| o.__send__(f, *a) }
      @observer_state = false
    end
  end
RUBY

$__stdlib["benchmark"] = <<~'RUBY'
  module Benchmark
    Tms = Struct.new(:real) do
      def total = real
      def utime = real
      def stime = 0.0
    end
    def self.realtime = (t = Time.now; yield; Time.now - t)
    def self.measure(*) = (t = Time.now; yield; Tms.new(Time.now - t))
    def self.bm(*) = yield(Struct.new(:x) { def report(*, &b) = (b&.call; nil) }.new(0))
  end
RUBY

$__stdlib["timeout"] = <<~'RUBY'
  module Timeout
    class Error < RuntimeError; end
    class ExitException < Exception; end
    def self.timeout(_sec, *_a) = yield(_sec)
  end
RUBY

$__stdlib["monitor"] = <<~'RUBY'
  class Monitor
    def synchronize = yield
    alias mon_synchronize synchronize
    def enter = nil
    def exit = nil
    def try_enter = true
    def new_cond = Object.new.tap { |o| def o.wait(*) = nil; def o.signal = nil; def o.broadcast = nil }
  end
  module MonitorMixin
    def mon_initialize = nil
    def synchronize = yield
    alias mon_synchronize synchronize
  end
RUBY

module Kernel
  alias_method :__susynth_require, :require
  def require(name)
    n = name.to_s.sub(/\.rb\z/, "")
    return false if $__stdlib_done[n]
    if (src = $__stdlib[n])
      $__stdlib_done[n] = true
      TOPLEVEL_BINDING.eval(src, "<#{n}>")
      return true
    end
    if $__stdlib_noop.include?(n)
      $__stdlib_done[n] = true
      return true
    end
    __susynth_require(name)
  end
end
