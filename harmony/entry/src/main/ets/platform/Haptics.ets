import { vibrator } from '@kit.SensorServiceKit';

/**
 * Light feedback for list gestures, as iOS `Haptics` (a light tap on folds and filters, success after a batch action).
 * It plays as touch vibration, so the phone's own touch-vibration setting turns it off; a phone without the effect,
 * or a refused vibration, simply gives no feedback.
 */
export class Haptics {
  private static supported: Map<string, boolean> = new Map<string, boolean>();

  static tap(): void { Haptics.play(vibrator.HapticFeedback.EFFECT_SOFT); }
  static success(): void { Haptics.play(vibrator.HapticFeedback.EFFECT_NOTICE_SUCCESS); }

  private static play(effect: string): void {
    try {
      let known = Haptics.supported.get(effect);
      if (known === undefined) { known = vibrator.isSupportEffectSync(effect); Haptics.supported.set(effect, known); }
      if (!known) return;
      vibrator.startVibration({ type: 'preset', effectId: effect, count: 1 }, { usage: 'touch' }).catch(() => undefined);
    } catch (_) { /* feedback is optional */ }
  }
}
