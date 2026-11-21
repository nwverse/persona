import { Colors } from '../design/Colors';
import { PatchLine, patchLines } from '../models/WorkspaceBrowser';

@ComponentV2
export struct WorkspacePatch {
  @Require @Param patch:string;
  @Param fileOnly:boolean = false;
  @Local available:number = 320;
  @Computed get lines():PatchLine[] { return patchLines(this.patch, this.fileOnly); }
  @Computed get contentWidth():number {
    const columns=this.lines.reduce((n:number,line:PatchLine) => Math.max(n,line.text.length),0);
    return Math.max(this.available,Math.min(12000,columns*8.2+84));
  }
  private ink(kind:string):ResourceColor {
    return kind==='added' ? Colors.positive : kind==='removed' ? Colors.danger : kind==='hunk' ? Colors.link : kind==='meta' ? Colors.secondary : Colors.ink;
  }
  build() {
    Scroll() {
      List() {
        Repeat<PatchLine>(this.lines).each((entry:RepeatItem<PatchLine>) => {
          ListItem() {
            Row({space:6}) {
              Text(entry.item.oldLine).width(30).fontSize(11).fontFamily('monospace').fontColor(Colors.secondary).textAlign(TextAlign.End)
              Text(entry.item.newLine).width(30).fontSize(11).fontFamily('monospace').fontColor(Colors.secondary).textAlign(TextAlign.End)
              Text(entry.item.text || ' ').layoutWeight(1).fontSize(13).fontFamily('monospace').fontColor(this.ink(entry.item.kind))
                .copyOption(CopyOptions.LocalDevice)
            }.width('100%').constraintSize({minHeight:20}).alignItems(VerticalAlign.Top)
              .backgroundColor(entry.item.kind==='added' ? '#182CAB69' : entry.item.kind==='removed' ? '#1CDA5350' : Color.Transparent)
          }
        }).key((line:PatchLine,index:number) => String(index)+':'+line.kind+':'+line.oldLine+':'+line.newLine+':'+line.text).virtualScroll({totalCount:this.lines.length,reusable:false})
      }.width(this.contentWidth).height('100%')
    }.scrollable(ScrollDirection.Horizontal).width('100%').height('100%').align(Alignment.TopStart)
      .onAreaChange((_:Area,a:Area) => this.available=Number(a.width)).id('session.git.patch')
  }
}
