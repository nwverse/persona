import { describe, it, expect } from '@ohos/hypium';
import { cryptoFramework } from '@kit.CryptoArchitectureKit';
import { RFBAuth } from '../../../main/ets/core/RFBAuth';
import { RFBZRLE } from '../../../main/ets/core/RFBZRLE';

function hex(bytes: Uint8Array): string { return Array.from(bytes).map((byte: number): string => byte.toString(16).padStart(2, '0')).join(''); }
function bytes(value: bigint, width: number): Uint8Array {
  const text = value.toString(16).padStart(width * 2, '0');
  const result = new Uint8Array(width);
  for (let index = 0; index < width; index++) result[index] = parseInt(text.slice(index * 2, index * 2 + 2), 16);
  return result;
}
export default function nativeRFBTests(): void {
  describe('NativeRFB', (): void => {
    it('ard_credentials_use_the_native_md5_and_aes_ecb_without_padding', 0, async (): Promise<void> => {
      const sealed = await RFBAuth.sealARD(new Uint8Array([13]), 'synthetic-user', 'synthetic-password', new Uint8Array(128).fill(170));
      expect(hex(sealed)).assertEqual('a14b16149b1da36dac529cc3bdcf33179ad934ce50d947549bb01b79928164e29ad934ce50d947549bb01b79928164e29ad934ce50d947549bb01b79928164e25519195228436b02acb9eb9c40779313819e1f6d84a3e8146e8f8ccc8f691cf19ad934ce50d947549bb01b79928164e29ad934ce50d947549bb01b79928164e2');
    });
    it('a_4096_bit_screen_sharing_peer_can_open_the_ard_reply', 0, async (): Promise<void> => {
      const params = cryptoFramework.DHKeyUtil.genDHCommonParamsSpec(4096);
      const server = await cryptoFramework.createAsyKeyGeneratorBySpec(params).generateKeyPair();
      try {
        const serverPublic = server.pubKey.getAsyKeySpec(cryptoFramework.AsyKeySpecItem.DH_PK_BN) as bigint;
        const reply = await RFBAuth.ard(bytes(params.g, 2), bytes(params.p, 512), bytes(serverPublic, 512), 'user', 'pass');
        expect(reply.publicKey.length).assertEqual(512);
        const spec: cryptoFramework.DHPubKeySpec = { algName: 'DH', specType: cryptoFramework.AsyKeySpecType.PUBLIC_KEY_SPEC,
          params: params, pk: BigInt('0x' + hex(reply.publicKey)) };
        const peer = await cryptoFramework.createAsyKeyGeneratorBySpec(spec).generatePubKey();
        const secret = await cryptoFramework.createKeyAgreement('DH').generateSecret(server.priKey, peer);
        const padded = new Uint8Array(512);
        padded.set(secret.data, 512 - secret.data.length);
        const digest = cryptoFramework.createMd('MD5');
        await digest.update({ data: padded });
        const key = await cryptoFramework.createSymKeyGenerator('AES128').convertKey(await digest.digest());
        try {
          const cipher = cryptoFramework.createCipher('AES128|ECB|NoPadding');
          await cipher.init(cryptoFramework.CryptoMode.DECRYPT_MODE, key, null);
          const block = (await cipher.doFinal({ data: reply.sealed })).data;
          expect(hex(block.subarray(0, 5))).assertEqual('7573657200');
          expect(hex(block.subarray(64, 69))).assertEqual('7061737300');
        } finally { key.clearMem(); }
      } finally { server.priKey.clearMem(); }
    });
    it('zrle_uses_one_native_inflater_across_sync_flushed_rectangles', 0, async (): Promise<void> => {
      const inflater = new RFBZRLE();
      try {
        const first = new Uint8Array([120,156,98,20,50,9,99,196,131,1,0,0,0,255,255]);
        const second = new Uint8Array([34,132,1,0,0,0,255,255]);
        const expected = '01123456'.repeat(8);
        expect(hex(await inflater.inflate(first, 64))).assertEqual(expected);
        expect(hex(await inflater.inflate(second, 64))).assertEqual(expected);
      } finally { inflater.close(); }
    });
    it('zrle_rejects_a_rectangle_that_exceeds_its_output_budget', 0, async (): Promise<void> => {
      const inflater = new RFBZRLE(); let failed = false;
      try { await inflater.inflate(new Uint8Array([120,156,98,20,50,9,99,196,131,1,0,0,0,255,255]), 8); }
      catch (_) { failed = true; } finally { inflater.close(); }
      expect(failed).assertTrue();
    });
  });
}
