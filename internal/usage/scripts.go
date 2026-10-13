package usage

import "github.com/redis/go-redis/v9"

// Validate types, integers and resulting counters first: Lua errors do not roll back writes.
const luaChecks = `
local MAX = 9007199254740991
local function integer(value)
  if type(value)=='string' and not string.match(value,'^%d+$') then error('invalid ledger integer syntax') end
  local n = tonumber(value)
  if not n or n < 0 or n > MAX or n ~= math.floor(n) then error('invalid ledger integer') end
  return n
end
local function hash(key)
  local t = redis.call('TYPE', key).ok
  if t ~= 'none' and t ~= 'hash' then error('invalid ledger key type') end
end
local function get(key, field, default)
  local raw = redis.call('HGET', key, field)
  if not raw then return default or 0 end
  return integer(raw)
end
local function add(a,b)
  if b > MAX-a then error('ledger overflow') end
  return a+b
end
local fields = {'requests','tokens_in','tokens_out','cost_nanos','estimated_requests','embedding_requests','embedding_tokens','embedding_cost_nanos'}
local function totals(key,kind,ti,to,cost,estimated)
  hash(key)
  local v={}
  for _,f in ipairs(fields) do v[f]=get(key,f) end
  if kind == 'chat' then
    v.requests=add(v.requests,1);v.tokens_in=add(v.tokens_in,ti);v.tokens_out=add(v.tokens_out,to)
    v.cost_nanos=add(v.cost_nanos,cost);v.estimated_requests=add(v.estimated_requests,estimated)
  else
    v.embedding_requests=add(v.embedding_requests,1);v.embedding_tokens=add(v.embedding_tokens,add(ti,to))
    v.embedding_cost_nanos=add(v.embedding_cost_nanos,cost)
  end
  add(add(v.tokens_in,v.tokens_out),v.embedding_tokens);add(v.cost_nanos,v.embedding_cost_nanos)
  return v
end
local function write_totals(key,v)
  for _,f in ipairs(fields) do redis.call('HSET',key,f,string.format('%.0f',v[f])) end
end
`

var reserveScript = redis.NewScript(luaChecks + `
for _,k in ipairs(KEYS) do hash(k) end
local old=redis.call('HGET',KEYS[3],ARGV[1])
if old then if old~=ARGV[2] then error('conflicting reservation ID') end;return 1 end
if redis.call('HEXISTS',KEYS[4],ARGV[1])==1 then error('completed reservation ID') end
local tokens=integer(ARGV[3]);local cost=integer(ARGV[4])
local limitT=get(KEYS[1],'tokens',integer(ARGV[5]));local limitC=get(KEYS[1],'cost_nanos',integer(ARGV[6]))
local rt=get(KEYS[2],'reserved_tokens');local rc=get(KEYS[2],'reserved_nanos')
local st=get(KEYS[2],'spent_tokens');local sc=get(KEYS[2],'spent_nanos')
if add(add(rt,st),tokens)>limitT or add(add(rc,sc),cost)>limitC then return 0 end
redis.call('HSET',KEYS[3],ARGV[1],ARGV[2])
redis.call('HSET',KEYS[2],'reserved_tokens',string.format('%.0f',add(rt,tokens)),'reserved_nanos',string.format('%.0f',add(rc,cost)))
return 1
`)

var recordScript = redis.NewScript(luaChecks + `
hash(KEYS[1]);hash(KEYS[2])
local old=redis.call('HGET',KEYS[1],ARGV[1])
if old then if old~=ARGV[2] then error('conflicting event ID') end;return 0 end
local v=totals(KEYS[2],ARGV[3],integer(ARGV[4]),integer(ARGV[5]),integer(ARGV[6]),integer(ARGV[7]))
redis.call('HSET',KEYS[1],ARGV[1],ARGV[2]);write_totals(KEYS[2],v)
return 1
`)

var settleScript = redis.NewScript(luaChecks + `
for _,k in ipairs(KEYS) do hash(k) end
local old=redis.call('HGET',KEYS[1],ARGV[1])
if old then if old~=ARGV[2] then error('conflicting event ID') end;return 0 end
local pending=redis.call('HGET',KEYS[3],ARGV[1])
if pending~=ARGV[3] then error('reservation missing or mismatched') end
local ti=integer(ARGV[5]);local to=integer(ARGV[6]);local cost=integer(ARGV[7])
local v=totals(KEYS[2],ARGV[4],ti,to,cost,integer(ARGV[8]))
local rt=get(KEYS[4],'reserved_tokens');local rc=get(KEYS[4],'reserved_nanos')
local st=get(KEYS[4],'spent_tokens');local sc=get(KEYS[4],'spent_nanos')
local reservedT=integer(ARGV[10]);local reservedC=integer(ARGV[11])
if rt<reservedT or rc<reservedC or add(ti,to)>reservedT or cost>reservedC then error('reservation accounting mismatch') end
local nt=add(st,add(ti,to));local nc=add(sc,cost)
redis.call('HSET',KEYS[1],ARGV[1],ARGV[2]);write_totals(KEYS[2],v)
if ARGV[9]=='1' then
  redis.call('HDEL',KEYS[3],ARGV[1])
  redis.call('HSET',KEYS[4],'reserved_tokens',string.format('%.0f',rt-reservedT),'reserved_nanos',string.format('%.0f',rc-reservedC),'spent_tokens',string.format('%.0f',nt),'spent_nanos',string.format('%.0f',nc))
end
return 1
`)
