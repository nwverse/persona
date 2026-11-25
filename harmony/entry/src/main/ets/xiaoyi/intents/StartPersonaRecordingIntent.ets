import { InsightIntentEntry, InsightIntentEntryExecutor, insightIntent } from '@kit.AbilityKit';
import { buildXiaoYiRouteUri, createXiaoYiRoute } from '../../core/XiaoYiRoutes';
import { tr } from '../../design/Strings';
import { PersonaActionResultEntity, errorIntentResult, okIntentResult } from './PersonaIntentEntities';

@InsightIntentEntry({
  intentName: 'StartPersonaRecording',
  domain: 'ProductivityDomain',
  intentVersion: '1.0.0',
  displayName: 'Start Meeting Recording',
  displayDescription: 'Open Persona directly to Meeting Recording',
  llmDescription: 'Open Persona in the foreground on the Meeting Recording screen so the user can start recording a meeting.',
  keywords: ['Record meeting', 'Start meeting', '开始会议录音', '用Persona录音', '会議を録音'],
  abilityName: 'EntryAbility',
  executeMode: [insightIntent.ExecuteMode.UI_ABILITY_FOREGROUND],
  parameters: {
    type: 'object',
    properties: {}
  },
  result: {
    type: 'object',
    properties: {
      entityId: { type: 'string' },
      summary: { type: 'string' },
      status: { type: 'string' },
      deepLinkUri: { type: 'string' }
    }
  }
})
export default class StartPersonaRecordingIntent extends InsightIntentEntryExecutor<PersonaActionResultEntity> {
  async onExecute(): Promise<insightIntent.IntentResult<PersonaActionResultEntity>> {
    const route = createXiaoYiRoute('recording');
    route.autoStart = true;
    const uri = buildXiaoYiRouteUri(route);
    try {
      await this.context.startAbility({
        abilityName: 'EntryAbility',
        action: 'ohos.want.action.viewData',
        uri: uri,
        parameters: {
          personaDest: 'recording',
          autoStart: true
        }
      });
      const msg = tr('xiaoyi_opening_recording');
      return okIntentResult(
        'recording:open',
        msg === 'xiaoyi_opening_recording' ? 'Opening Persona Meeting Recording.' : msg,
        'ok',
        uri
      );
    } catch (error) {
      return errorIntentResult('recording:error', error as Error, uri);
    }
  }
}
