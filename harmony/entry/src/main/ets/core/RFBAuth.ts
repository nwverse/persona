import { cryptoFramework } from '@kit.CryptoArchitectureKit';
import { util } from '@kit.ArkTS';

export class RFBCredentials {
  account: string = '';
  password: string = '';
  constructor(account: string = '', password: string = '') { this.account = account; this.password = password; }
}

export class RFBARDReply {
  sealed: Uint8Array = new Uint8Array(0);
  publicKey: Uint8Array = new Uint8Array(0);
  constructor(sealed: Uint8Array, publicKey: Uint8Array) { this.sealed = sealed; this.publicKey = publicKey; }
}

function unsignedValue(bytes: Uint8Array): bigint {
  return BigInt('0x' + Array.from(bytes).map((byte: number): string => byte.toString(16).padStart(2, '0')).join(''));
}

function publicKeyBytes(value: bigint, width: number): Uint8Array {
  const hex: string = value.toString(16).padStart(width * 2, '0');
  if (hex.length !== width * 2) throw new Error('rfb_crypto');
  const bytes = new Uint8Array(width);
  for (let index: number = 0; index < width; index++) bytes[index] = parseInt(hex.slice(index * 2, index * 2 + 2), 16);
  return bytes;
}

/** Security types and credential encodings used by the Apple RFB client. */
export class RFBAuth {
  static readonly none: number = 1;
  static readonly veNCrypt: number = 19;
  static readonly securityARD: number = 30;
  static readonly plain: number = 256;
  static readonly maxPrimeBytes: number = 512;
  static readonly preferred: number[] = [RFBAuth.securityARD, RFBAuth.veNCrypt, RFBAuth.none];

  static be32(value: number): Uint8Array {
    return new Uint8Array([(value >>> 24) & 255, (value >>> 16) & 255, (value >>> 8) & 255, value & 255]);
  }

  static plainCredentials(account: string, password: string): Uint8Array {
    const encoder = new util.TextEncoder();
    const user: Uint8Array = encoder.encodeInto(account);
    const pass: Uint8Array = encoder.encodeInto(password);
    const out = new Uint8Array(8 + user.length + pass.length);
    out.set(RFBAuth.be32(user.length), 0);
    out.set(RFBAuth.be32(pass.length), 4);
    out.set(user, 8);
    out.set(pass, 8 + user.length);
    user.fill(0);
    pass.fill(0);
    return out;
  }

  static async ard(generator: Uint8Array, prime: Uint8Array, serverKey: Uint8Array, account: string, password: string): Promise<RFBARDReply> {
    if (prime.length < 1 || prime.length > RFBAuth.maxPrimeBytes || (prime[prime.length - 1] & 1) !== 1 ||
      generator.length < 1 || generator.length > 4 || serverKey.length !== prime.length)
      throw new Error('rfb_crypto');
    // Screen Sharing supplies its own DH group. Keep the two large exponentiations in the async native provider.
    const params: cryptoFramework.DHCommonParamsSpec = { algName: 'DH', specType: cryptoFramework.AsyKeySpecType.COMMON_PARAMS_SPEC,
      p: unsignedValue(prime), g: unsignedValue(generator), l: 0 };
    const peerSpec: cryptoFramework.DHPubKeySpec = { algName: 'DH', specType: cryptoFramework.AsyKeySpecType.PUBLIC_KEY_SPEC,
      params: params, pk: unsignedValue(serverKey) };
    const peer = await cryptoFramework.createAsyKeyGeneratorBySpec(peerSpec).generatePubKey();
    const pair = await cryptoFramework.createAsyKeyGeneratorBySpec(params).generateKeyPair();
    const shared = new Uint8Array(prime.length);
    try {
      const secret = await cryptoFramework.createKeyAgreement('DH').generateSecret(pair.priKey, peer);
      try {
        if (!secret.data.length || secret.data.length > shared.length) throw new Error('rfb_crypto');
        shared.set(secret.data, shared.length - secret.data.length);
      } finally { secret.data.fill(0); }
      const publicValue = pair.pubKey.getAsyKeySpec(cryptoFramework.AsyKeySpecItem.DH_PK_BN) as bigint;
      const padding = await cryptoFramework.createRandom().generateRandom(128);
      const sealed = await RFBAuth.sealARD(shared, account, password, padding.data);
      return new RFBARDReply(sealed, publicKeyBytes(publicValue, prime.length));
    } finally {
      pair.priKey.clearMem();
      shared.fill(0);
    }
  }

  static async sealARD(shared: Uint8Array, account: string, password: string, block: Uint8Array): Promise<Uint8Array> {
    if (block.length !== 128) throw new Error('rfb_crypto');
    const digest = cryptoFramework.createMd('MD5');
    await digest.update({ data: shared });
    const keyBytes = (await digest.digest()).data;
    const encoder = new util.TextEncoder();
    const user = encoder.encodeInto(account);
    const pass = encoder.encodeInto(password);
    try {
      block.set(user.subarray(0, 63), 0);
      block[Math.min(user.length, 63)] = 0;
      block.set(pass.subarray(0, 63), 64);
      block[64 + Math.min(pass.length, 63)] = 0;
      const key = await cryptoFramework.createSymKeyGenerator('AES128').convertKey({ data: keyBytes });
      try {
        const cipher = cryptoFramework.createCipher('AES128|ECB|NoPadding');
        await cipher.init(cryptoFramework.CryptoMode.ENCRYPT_MODE, key, null);
        return (await cipher.doFinal({ data: block })).data;
      } finally { key.clearMem(); }
    } finally {
      keyBytes.fill(0); block.fill(0); user.fill(0); pass.fill(0);
    }
  }
}
