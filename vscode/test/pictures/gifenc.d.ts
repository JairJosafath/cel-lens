// gifenc ships no types; these are the parts take.ts uses.
declare module 'gifenc' {
  export type Palette = number[][];

  export interface GifEncoder {
    writeFrame(
      index: Uint8Array,
      width: number,
      height: number,
      options: { palette: Palette; delay: number },
    ): void;
    finish(): void;
    bytes(): Uint8Array;
  }

  const gifenc: {
    GIFEncoder: () => GifEncoder;
    quantize: (rgba: Uint8Array | Uint8ClampedArray, maxColors: number) => Palette;
    applyPalette: (rgba: Uint8Array | Uint8ClampedArray, palette: Palette) => Uint8Array;
  };
  export default gifenc;
}
