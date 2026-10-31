const assert = require('node:assert/strict');
const { window: snip } = require('../static/snip-window.js');
const seg = (i, start, end) => ({ id: 's' + i, start, end });
const segs = Array.from({ length: 20 }, (_, i) => seg(i, i * 5, i * 5 + 5)); // 每句 5 秒

// 中间位置：向前约 20 秒（4 句）、向后约 10 秒（2 句）。
assert.deepEqual(snip(segs, 52), ['s6', 's7', 's8', 's9', 's10', 's11', 's12']);
// 开头：不越界。
assert.deepEqual(snip(segs, 1), ['s0', 's1', 's2']);
// 结尾：不越界。
assert.deepEqual(snip(segs, 99), ['s15', 's16', 's17', 's18', 's19']);
// 句间停顿：取刚结束的那一句。
const gappy = [seg(0, 0, 4), seg(1, 10, 14)];
assert.deepEqual(snip(gappy, 7), ['s0', 's1']);
// 早于第一句：没有可用位置。
assert.deepEqual(snip([seg(0, 10, 14)], 3), []);
// 每侧最多 4 句：句子很短时也不会无限扩展。
const short = Array.from({ length: 40 }, (_, i) => seg(i, i, i + 1));
const out = snip(short, 20.5);
assert.equal(out.length, 9);
assert.equal(out[0], 's16');
// 空输入与非法输入。
assert.deepEqual(snip([], 5), []);
assert.deepEqual(snip(null, 5), []);
// 边界：位置恰为一句的结束时刻，属于下一句。
assert.equal(snip(segs, 10).includes('s2'), true);
console.log('ok');
