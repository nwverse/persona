import { common, skillManager } from '@kit.AbilityKit';
import { relationalStore } from '@kit.ArkData';
import { util } from '@kit.ArkTS';
import { describe, expect, it } from '@ohos/hypium';
import {
  buildInlineCardResolution,
  nextInlineConfirmStep,
  parseAgentCardPayload
} from '../../../main/ets/core/AgentCardPayload';
import { parseShareRecords } from '../../../main/ets/core/SharePayload';
import {
  A2AArtifactUpdate,
  A2AAskResult,
  A2AContextBinding,
  A2AHubBridge,
  A2AKnowledgeHit,
  A2ASendTaskResult,
  A2ATaskSink,
  A2ATaskState,
  A2ATaskStateValue,
  A2ATodoResult,
  A2AWaterResult,
  XiaoYiA2AEngine
} from '../../../main/ets/core/XiaoYiA2AEngine';
import { XiaoYiStatusSummary } from '../../../main/ets/core/XiaoYiActions';
import { buildXiaoYiRouteUri, createXiaoYiRoute, parseXiaoYiUri } from '../../../main/ets/core/XiaoYiRoutes';
import { CredentialVault } from '../../../main/ets/platform/CredentialVault';
import { LocalStore } from '../../../main/ets/platform/LocalStore';
import { nativeTestContext } from './NativeContext';

class StubA2ABridge implements A2AHubBridge {
  interrupted: string = '';

  async askPersona(
    question: string,
    _conversationId: string,
    _machineId: string,
    onProgress: (text: string, done: boolean) => void,
    _isCancelled: () => boolean
  ): Promise<A2AAskResult> {
    const reply = 'Echo: ' + question;
    onProgress('Echo: ', false);
    onProgress(reply, true);
    return {
      answer: reply,
      pending: false,
      machineId: 'm-native',
      sessionId: 's-native',
      deepLinkUri: 'persona://open?dest=session&machine=m-native&session=s-native',
      askUserPrompt: ''
    };
  }

  async checkStatus(): Promise<XiaoYiStatusSummary> {
    return {
      workingCount: 1,
      pendingCount: 1,
      unreadPersonalCount: 0,
      topPending: {
        id: 'native-dec-1',
        machineId: 'm-native',
        sessionId: 's-native',
        kind: 'permission',
        status: 'open',
        summary: 'Run verification check',
        toolName: 'Bash',
        createdAt: '2026-11-26T06:00:00Z',
        delegating: false,
        muted: false,
        quiet: false,
        planThen: '',
        hasRequiredFields: false
      },
      headline: '1 working, 1 need you.',
      detail: 'First pending: Run verification check',
      spokenText: '1 working, 1 need you. First pending: Run verification check'
    };
  }

  async addTodo(title: string, _notes: string): Promise<A2ATodoResult> {
    return { id: 'todo-native', title: title, deepLinkUri: 'persona://open?dest=todos' };
  }

  async logWater(amountMl?: number): Promise<A2AWaterResult> {
    return { recordedMl: amountMl ?? 250, todayMl: 500, goalMl: 2000, deepLinkUri: 'persona://open?dest=health' };
  }

  async searchKnowledge(_query: string): Promise<A2AKnowledgeHit[]> {
    return [{ path: 'docs/xiaoyi.md', line: 'Native test hit' }];
  }

  async sendAgentTask(_prompt: string, _machineName: string, _sessionName: string): Promise<A2ASendTaskResult> {
    return {
      machineId: 'm-native',
      machineName: 'Studio',
      sessionId: 's-native',
      sessionTitle: 'Native',
      deepLinkUri: 'persona://open?dest=session&machine=m-native&session=s-native'
    };
  }

  async interruptSession(machineId: string, sessionId: string): Promise<void> {
    this.interrupted = machineId + '/' + sessionId;
  }

  async getContextConversation(_contextId: string): Promise<A2AContextBinding | undefined> {
    return undefined;
  }

  async setContextConversation(_contextId: string, _machineId: string, _sessionId: string): Promise<void> {}

  formatError(error: Error): string {
    return error.message || 'failed';
  }
}

class RecordingSink implements A2ATaskSink {
  states: A2ATaskStateValue[] = [];
  artifacts: A2AArtifactUpdate[] = [];

  updateStatus(_taskId: string, state: A2ATaskStateValue, _messageText?: string): void {
    this.states.push(state);
  }

  addArtifact(_taskId: string, artifact: A2AArtifactUpdate): void {
    this.artifacts.push(artifact);
  }
}

export default function nativeXiaoYiTests(): void {
  describe('NativeXiaoYi', (): void => {
    it('application_context_supports_credential_vault_local_store_and_xiaoyi_purge', 0, async (): Promise<void> => {
      const appContext: common.Context = nativeTestContext().getApplicationContext();
      const dbName = 'xiaoyi-native-' + util.generateRandomUUID() + '.db';
      const hubId = 'xiaoyi-hub-' + util.generateRandomUUID();
      const vault = new CredentialVault(appContext);
      const store = new LocalStore(dbName);
      const ownKey = 'xiaoyi:' + JSON.stringify([hubId, 'personal']) + ':a2a-contexts';
      const otherKey = 'xiaoyi:' + JSON.stringify([hubId + '-other', 'personal']) + ':a2a-contexts';
      try {
        await vault.put(hubId, 'synthetic-xiaoyi-token');
        expect(await vault.get(hubId)).assertEqual('synthetic-xiaoyi-token');

        await store.open(appContext);
        await store.put(ownKey, '{"ctx":"s1"}');
        await store.put(otherKey, '{"ctx":"s2"}');
        expect(await store.get(ownKey)).assertEqual('{"ctx":"s1"}');

        await store.purgeHub(hubId);
        expect(await store.get(ownKey)).assertEqual('');
        expect(await store.get(otherKey)).assertEqual('{"ctx":"s2"}');
      } finally {
        await vault.remove(hubId);
        await store.close();
        await relationalStore.deleteRdbStore(appContext, dbName);
      }
    });

    it('skill_manager_discovers_declared_persona_skills_when_available', 0, async (): Promise<void> => {
      const flags = skillManager.SkillInfoFlag.GET_SKILL_INFO_WITH_DESCRIPTION |
        skillManager.SkillInfoFlag.GET_SKILL_INFO_WITH_SRC_ENTRIES;
      const infos = await skillManager.getSkillInfosForSelf(flags);
      const names = infos.map((info: skillManager.SkillInfo): string => info.skillName);
      expect(names.includes('persona-assistant')).assertTrue();
      expect(names.includes('persona-workspace')).assertTrue();
    });

    it('a2a_engine_executes_streaming_approvals_and_perception_suggest_on_arkts', 0, async (): Promise<void> => {
      const engine = new XiaoYiA2AEngine(new StubA2ABridge());
      const sinkExec = new RecordingSink();
      await engine.handleMethod('Execute', {
        taskId: 'native-t1',
        contextId: 'native-c1',
        userInput: 'Hello Persona',
        parts: [{ text: 'Hello Persona' }],
        locale: 'en'
      }, sinkExec);
      expect(sinkExec.states[0]).assertEqual(A2ATaskState.SUBMITTED);
      expect(sinkExec.states[1]).assertEqual(A2ATaskState.WORKING);
      expect(sinkExec.states[sinkExec.states.length - 1]).assertEqual(A2ATaskState.COMPLETED);
      expect(sinkExec.artifacts.length).assertEqual(2);

      const sinkSuggest = new RecordingSink();
      await engine.handleMethod('PerceptionSuggest', {
        taskId: 'native-t2',
        contextId: '',
        userInput: '',
        parts: [],
        locale: 'zh-CN'
      }, sinkSuggest);
      expect(sinkSuggest.states[sinkSuggest.states.length - 1]).assertEqual(A2ATaskState.COMPLETED);
      expect(sinkSuggest.artifacts.length).assertEqual(1);
    });

    it('deep_link_routes_share_payload_and_agent_card_round_trip', 0, (): void => {
      const route = createXiaoYiRoute('session');
      route.profile = 'hub-1';
      route.manager = 'personal';
      route.machine = 'm1';
      route.session = 's1';
      const uri = buildXiaoYiRouteUri(route);
      const decoded = parseXiaoYiUri(uri);
      expect(decoded !== undefined).assertTrue();
      expect(decoded?.dest).assertEqual('session');
      expect(decoded?.session).assertEqual('s1');

      const shared = parseShareRecords([{ title: 'Note', content: 'Body', uri: 'https://example.com/a' }]);
      expect(shared.hasContent).assertTrue();
      expect(shared.route?.dest).assertEqual('share');

      const card = parseAgentCardPayload({
        cardType: 'approval',
        decisionId: 'd1',
        decisionKind: 'plan',
        canApproveInline: true
      });
      expect(card.canApproveInline).assertTrue();
      expect(buildInlineCardResolution('plan') !== undefined).assertTrue();
      expect(nextInlineConfirmStep('idle', 'request')).assertEqual('confirming');
    });
  });
}
