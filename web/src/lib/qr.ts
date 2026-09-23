import QRCode from 'qrcode';

// Encodes only the same resolved public URL used by Copy link. No HTTP request.
export async function eventQR(publicURL: string, canvas?: HTMLCanvasElement): Promise<string> {
  const url = new URL(publicURL);
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) throw new Error('Invalid public event URL');
  const options = { errorCorrectionLevel: 'M' as const, margin: 4, width: 1024, color: { dark: '#000000ff', light: '#ffffffff' } };
  if (canvas) {
    await QRCode.toCanvas(canvas, publicURL, options);
    // The encoder sets inline pixel dimensions. Let responsive CSS size the
    // display while keeping the downloaded bitmap at its full resolution.
    canvas.style.removeProperty('width');
    canvas.style.removeProperty('height');
    return canvas.toDataURL('image/png');
  }
  return QRCode.toDataURL(publicURL, options);
}
