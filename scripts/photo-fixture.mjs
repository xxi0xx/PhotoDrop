// Generate tiny valid RGB PNGs for integration tests; no media processing is
// imported into the production application.
import { randomInt } from 'node:crypto';
import { deflateSync } from 'node:zlib';
let pixel = randomInt(0x1000000);
function chunk(name, data) {
  const type = Buffer.from(name), n = Buffer.alloc(4); n.writeUInt32BE(data.length);
  const body = Buffer.concat([type, data]); let crc = 0xffffffff;
  for (const byte of body) { crc ^= byte; for (let bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ ((crc & 1) ? 0xedb88320 : 0); }
  const checksum = Buffer.alloc(4); checksum.writeUInt32BE((crc ^ 0xffffffff) >>> 0);
  return Buffer.concat([n, body, checksum]);
}
export function photo() {
  pixel = (pixel + 1) & 0xffffff;
  const header = Buffer.alloc(13); header.writeUInt32BE(1, 0); header.writeUInt32BE(1, 4); header[8] = 8; header[9] = 2;
  return Buffer.concat([Buffer.from('89504e470d0a1a0a', 'hex'), chunk('IHDR', header), chunk('IDAT', deflateSync(Buffer.from([0, pixel >> 16, (pixel >> 8) & 255, pixel & 255]))), chunk('IEND', Buffer.alloc(0))]);
}
