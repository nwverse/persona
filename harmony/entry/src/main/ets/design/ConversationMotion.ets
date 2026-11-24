import { nativeEnvironment } from '../platform/NativeEnvironment';

/** Only the contents fade and move. The list takes its final height immediately to preserve its reading anchor. */
export function conversationDisclosure(): TransitionEffect {
  if (nativeEnvironment.reduceMotion) return TransitionEffect.IDENTITY;
  const enter = TransitionEffect.OPACITY.combine(TransitionEffect.translate({ y: -3 }))
    .animation({ duration: 160, curve: Curve.EaseOut });
  const exit = TransitionEffect.OPACITY.animation({ duration: 90, curve: Curve.EaseIn });
  return TransitionEffect.asymmetric(enter, exit);
}

export function conversationMarkFade(): TransitionEffect {
  return nativeEnvironment.reduceMotion ? TransitionEffect.IDENTITY :
    TransitionEffect.OPACITY.animation({ duration: 140, curve: Curve.EaseOut });
}
