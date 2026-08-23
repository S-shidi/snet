declare module "qrcode-generator" {
  function qrcode(typeNumber: number, errorCorrectionLevel: string): {
    addData(data: string): void;
    make(): void;
    createSvgTag(opts: { cellSize: number; margin: number; scalable: boolean }): string;
  };
  export default qrcode;
}
