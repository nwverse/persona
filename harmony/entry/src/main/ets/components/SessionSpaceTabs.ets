import { FrameCallback } from '@kit.ArkUI';
import { Colors } from '../design/Colors';
import { Symbols } from '../design/Symbols';
import { tr } from '../design/Strings';
import { Tokens } from '../design/Tokens';
import { SessionSpace, SessionSpaceTab } from '../models/SessionSpace';
import { nativeEnvironment } from '../platform/NativeEnvironment';

class TabLayoutFrame extends FrameCallback {
  private action:() => void;
  constructor(action:() => void) { super(); this.action = action; }
  onFrame(_time:number):void { this.action(); }
}

@ComponentV2
export struct SessionSpaceTabs {
  @Require @Param space:SessionSpace;
  @Event onPick:(key:string,from:string,id:string) => void = () => {};
  private scroller:Scroller = new Scroller();
  private centered:string = '';
  private active:boolean = false;
  aboutToAppear():void { this.active = true; }
  aboutToDisappear():void { this.active = false; this.centered = ''; }
  @Monitor('space') dataChanged():void { this.centerCurrent(); }
  private centerCurrent():void {
    if (!this.active) return;
    const index = this.space.tabs.findIndex((tab:SessionSpaceTab) => tab.id === this.space.current);
    const signature = JSON.stringify([this.space.key,this.space.current,index,nativeEnvironment.fontScale,nativeEnvironment.windowWidthPx]);
    if (index < 0 || this.centered === signature) return;
    this.centered = signature;
    this.getUIContext().postFrameCallback(new TabLayoutFrame(() => {
      if (this.active && this.centered === signature) this.scroller.scrollToIndex(index,false,ScrollAlign.CENTER);
    }));
  }
  private ink(tab:SessionSpaceTab):ResourceColor {
    if (tab.state === 'needs_you' || tab.state === 'blocked') return Colors.needs;
    if (tab.state === 'working') return Colors.working;
    if (tab.state === 'unread' || tab.state === 'done') return Colors.unread;
    return Colors.ink;
  }
  private fill(tab:SessionSpaceTab):ResourceColor {
    if (tab.state === 'needs_you' || tab.state === 'blocked') return nativeEnvironment.dark ? '#44318AFF' : '#22318AFF';
    if (tab.state === 'working') return nativeEnvironment.dark ? '#49F49B37' : '#24F49B37';
    if (tab.state === 'unread' || tab.state === 'done') return nativeEnvironment.dark ? '#4148B78C' : '#2248B78C';
    return Colors.control;
  }
  @Builder chip(tab:SessionSpaceTab,spaceKey:string,from:string) {
    Button({type:ButtonType.Capsule}) {
      Row({space:4}) {
        Text(tab.label).fontSize(13).maxFontScale(Tokens.navigationTextMaxScale).fontWeight(tab.id === from ? FontWeight.Bold : FontWeight.Medium)
          .fontColor(this.ink(tab)).maxLines(1).constraintSize({maxWidth:nativeEnvironment.fontScale >= 1.5 ? 204 : 152})
          .textOverflow({overflow:TextOverflow.Ellipsis})
        if (tab.starter) Text(tr('session_space_starter')).fontSize(11).maxFontScale(Tokens.navigationTextMaxScale).fontColor(Colors.secondary).maxLines(1)
      }.padding({left:10,right:10,top:4,bottom:4}).constraintSize({minHeight:30}).backgroundColor(this.fill(tab))
        .border({width:tab.id === from ? 1.3 : 0,color:this.ink(tab),radius:20}).borderRadius(20)
    }.height(44).constraintSize({minWidth:44}).padding(0).backgroundColor(Color.Transparent)
      .accessibilityText(tab.label+' · '+(tab.row.title || tab.id)).accessibilitySelected(tab.id === from)
      .onClick(() => { if (tab.id !== from) this.onPick(spaceKey,from,tab.id); }).id('session.sibling.'+tab.id)
  }
  @Builder picker(spaceKey:string,from:string) {
    Menu() {
      ForEach(this.space.tabs,(tab:SessionSpaceTab) => {
        MenuItem({content:tab.label,labelInfo:tab.row.title}).selectIcon(true).selected(tab.id === from)
          .onClick(() => { if (tab.id !== from) this.onPick(spaceKey,from,tab.id); })
      },(tab:SessionSpaceTab) => JSON.stringify([tab.id,tab.label,tab.row.title,spaceKey,from]))
    }
  }
  build() {
    Row({space:6}) {
      List({space:6,scroller:this.scroller}) {
        ForEach(this.space.tabs,(tab:SessionSpaceTab) => {
          ListItem() { this.chip(tab,this.space.key,this.space.current) }
        },(tab:SessionSpaceTab) => JSON.stringify([this.space.key,this.space.current,tab.id,tab.label,tab.row.title,tab.state,tab.starter]))
      }.listDirection(Axis.Horizontal).scrollBar(BarState.Off).edgeEffect(EdgeEffect.None).layoutWeight(1)
        .height(44)
        .onAreaChange(() => this.centerCurrent()).id('session.siblings')
      Button({type:ButtonType.Capsule}) {
        Row({space:4}) {
          SymbolGlyph(Symbols.folder).fontSize('14vp').fontColor([Colors.secondary]).flexShrink(0)
          Text(String(this.space.tabs.length)).fontSize(13).maxFontScale(Tokens.navigationTextMaxScale).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
        }.padding({left:10,right:10,top:4,bottom:4}).constraintSize({minHeight:30}).backgroundColor(Colors.control).borderRadius(20)
      }.height(44).constraintSize({minWidth:44}).padding(0).backgroundColor(Color.Transparent).flexShrink(0)
        .accessibilityText(tr('session_space_agents').replace('{count}',String(this.space.tabs.length)))
        .bindMenu(this.picker(this.space.key,this.space.current)).id('session.siblings.all')
    }.width('100%').padding({left:12,right:12})
  }
}
