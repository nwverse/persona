#!/usr/bin/env python3
"""Draw the running-tool animations used by ToolGlyph (`tool_motion_*.webp`).

Original Persona artwork based on the outlines in `ToolGlyphPaths.ts`: each sheet
holds ten 64 px alpha-only cells per row on a 60 Hz timeline; the app tints them
with the current ink. Needs Pillow with WebP support.

    python3 harmony/scripts/make-tool-motion.py harmony/entry/src/main/resources/base/media [name ...]

Frame counts and durations must match the `sheets` table in `ets/design/ToolGlyph.ets`.
"""
import math, sys, os
from PIL import Image, ImageDraw, ImageChops
OUT=sys.argv[1]; SS=512; CELL=64; U=SS/24.0; W=1.8
SPEC={'command':(100,1667),'search':(72,1200),'read':(36,600),'web':(100,1667),'scan':(100,1667),
      'computer':(180,3000),'knowledge':(72,1200),'memory':(90,1500),'generate':(240,4000)}
def ease(x): x=max(0,min(1,x)); return x*x*(3-2*x)
class C:
    def __init__(s): s.im=Image.new('L',(SS,SS),0)
    def line(s,pts,w=W,a=1.0,closed=False):
        if a<=0.004 or len(pts)<1: return
        lay=Image.new('L',(SS,SS),0); d=ImageDraw.Draw(lay); v=int(255*min(1,a)); r=w*U/2
        P=[(x*U,y*U) for x,y in pts]+([ (pts[0][0]*U,pts[0][1]*U)] if closed else [])
        if len(P)>1: d.line(P,fill=v,width=max(1,int(round(w*U))))
        for x,y in P: d.ellipse((x-r,y-r,x+r,y+r),fill=v)
        s.im=ImageChops.lighter(s.im,lay)
    def dot(s,x,y,r,a=1.0):
        lay=Image.new('L',(SS,SS),0); ImageDraw.Draw(lay).ellipse(((x-r)*U,(y-r)*U,(x+r)*U,(y+r)*U),fill=int(255*min(1,a))); s.im=ImageChops.lighter(s.im,lay)
def arc(cx,cy,rx,ry,a0=0,a1=2*math.pi,n=72): return [(cx+rx*math.cos(a0+(a1-a0)*i/n),cy+ry*math.sin(a0+(a1-a0)*i/n)) for i in range(n+1)]
def rrect(x0,y0,x1,y1,r):
    p=[]
    for cx,cy,a in ((x1-r,y0+r,-math.pi/2),(x1-r,y1-r,0),(x0+r,y1-r,math.pi/2),(x0+r,y0+r,math.pi)):
        p+=arc(cx,cy,r,r,a,a+math.pi/2,10)
    return p
def quad(p0,p1,p2,n=24): return [((1-t)**2*p0[0]+2*(1-t)*t*p1[0]+t*t*p2[0],(1-t)**2*p0[1]+2*(1-t)*t*p1[1]+t*t*p2[1]) for t in (i/n for i in range(n+1))]

def command(c,t):
    c.line(rrect(2.5,4.5,21.5,19.5,3),closed=True)
    k=0.7*math.sin(math.pi*ease(t/0.5)) if t<0.5 else 0
    c.line([(6.5+k,9),(9.7+k,12),(6.5+k,15)])
    if t<0.55: L=4*ease(t/0.55); a=1
    elif t<0.8: L=4; a=1
    else: L=4; a=1-ease((t-0.8)/0.2)
    if L>0.05: c.line([(12.5,15),(12.5+L,15)],a=a)
def search(c,t):
    ang=2*math.pi*t; cx=10.5+1.1*math.cos(ang); cy=10.5+1.1*math.sin(ang)
    c.line(arc(cx,cy,6.7,6.7),closed=True)
    ex,ey=cx+6.7*0.7071,cy+6.7*0.7071; c.line([(ex-1.3,ey-1.3),(ex+3.9,ey+3.9)])
    gl=2*math.pi*t+math.pi*0.9; c.line(arc(cx,cy,3.9,3.9,gl,gl+0.9,12),w=1.4,a=0.55)
def read(c,t):
    c.line(quad((12,6.2),(7.2,3.6),(2.7,4.6))+[(2.7,18.7)]+quad((2.7,18.7),(7.9,17.8),(12,20.3))[1:])
    c.line(quad((12,6.2),(16.8,3.6),(21.3,4.6))+[(21.3,18.7)]+quad((21.3,18.7),(16.1,17.8),(12,20.3))[1:])
    c.line([(12,6.2),(12,20.3)])
    x=12+8.6*math.cos(math.pi*ease(t)); lift=2.4*math.sin(math.pi*ease(t))
    top=quad((12,6.2),((12+x)/2,4.4-lift),(x,4.9-lift*0.6)); bot=quad((x,18.9-lift*0.6),((12+x)/2,18.4-lift),(12,20.3))
    c.line(top+bot,w=1.6,a=0.35+0.65*math.sin(math.pi*ease(t)))
def web(c,t):
    c.line(arc(12,12,9,9),closed=True); c.line([(3.5,12),(20.5,12)])
    for ph in (0,0.5):
        rx=4.6*math.cos(2*math.pi*(t+ph)); a=0.35+0.65*max(0,math.sin(2*math.pi*(t+ph)))
        if abs(rx)<0.25: c.line([(12,3),(12,21)],a=a)
        else: c.line(arc(12,12,abs(rx),9),a=a,closed=True)
def scan(c,t):
    for sx,sy in ((1,1),(-1,1),(1,-1),(-1,-1)):
        x=12-sx*8.5; y=12-sy*8.5; c.line([(x,y+sy*4),(x,y),(x+sx*4,y)])
    y=7+10*(0.5-0.5*math.cos(2*math.pi*t)); c.line([(6.5,y),(17.5,y)])
    tr=-1 if math.sin(2*math.pi*t)>0 else 1; c.line([(7.5,y+tr*1.6),(16.5,y+tr*1.6)],w=1.2,a=0.3)
def arrow(c,x,y,s=1.0):
    p=[(0,0),(0,4.6),(1.25,3.45),(2.15,5.3),(3.0,4.9),(2.1,3.1),(3.7,3.1)]
    c.line([(x+px*s,y+py*s) for px,py in p],w=1.3,closed=True)
def computer(c,t):
    c.line(rrect(2.5,3.5,21.5,16.5,2.3),closed=True); c.line([(12,16.5),(12,19.8)]); c.line([(8.5,20),(15.5,20)])
    stops=[(6.2,6.2),(13.8,8.2),(9.0,10.6)]; n=len(stops); seg=t*n; i=int(seg)%n; f=seg-int(seg)
    a,b=stops[i],stops[(i+1)%n]
    if f<0.55: m=ease(f/0.55); x=a[0]+(b[0]-a[0])*m; y=a[1]+(b[1]-a[1])*m; s=1
    else:
        x,y=b; g=(f-0.55)/0.45; s=1-0.12*math.sin(math.pi*min(1,g/0.35)) if g<0.35 else 1
        r=0.6+3.0*ease(g); c.line(arc(x,y,r,r),w=1.2,a=0.8*(1-ease(g)),closed=True)
    arrow(c,x,y,s)
def knowledge(c,t):
    c.line(rrect(4.5,3,19.5,21,2.6),closed=True)
    for k,(yy,L) in enumerate(((8,8.0),(12,6.5),(16,7.6))):
        ph=(t*3-k)%3; on=math.sin(math.pi*min(1,ph)) if ph<1 else 0
        c.line([(8,yy),(8+L,yy)],a=0.35+0.65*on)
        if on>0.02: c.dot(8+L*ease(min(1,ph)) ,yy,0.0+1.05*on,a=on)
def memory(c,t):
    c.line(rrect(6,6,18,18,2.6),closed=True)
    pins=[]
    for k in range(3):
        o=9+3*k; pins+= [((o,6),(o,3.6)),((18,o),(20.4,o)),((24-o,18),(24-o,20.4)),((6,24-o),(3.6,24-o))]
    order=[0,3,6,9,1,4,7,10,2,5,8,11]  # walk around
    for idx,(p,q) in enumerate(pins):
        pos=order.index(idx)/12; d=(t-pos)%1; on=max(0,1-d*4)
        c.line([p,q],a=0.4+0.6*on)
    g=t; r=1.0+3.2*ease(g); c.line(arc(12,12,r,r),w=1.4,a=0.9*(1-ease(g)),closed=True); c.dot(12,12,1.1)
def star(cx,cy,R,rot,k=0.30):
    pts=[]
    for i in range(4):
        a=rot+i*math.pi/2; b=a+math.pi/2
        p0=(cx+R*math.cos(a),cy+R*math.sin(a)); p2=(cx+R*math.cos(b),cy+R*math.sin(b))
        m=a+math.pi/4; ctl=(cx+R*k*math.cos(m),cy+R*k*math.sin(m))
        pts+=quad(p0,ctl,p2,16)[:-1]
    return pts
def generate(c,t):
    s=0.86+0.14*(0.5+0.5*math.cos(2*math.pi*t)); tw=0.06*math.sin(2*math.pi*t)
    c.line(star(12,12.2,9.0*s,-math.pi/2+tw,0.16),closed=True)
    for cx,cy,ph in ((19.2,4.8,0.15),(4.6,19.0,0.6)):
        d=(t-ph)%1; on=math.sin(math.pi*d/0.35) if d<0.35 else 0
        if on>0.02:
            r=1.9*on; c.line([(cx-r,cy),(cx+r,cy)],w=1.4,a=on); c.line([(cx,cy-r),(cx,cy+r)],w=1.4,a=on)
FN={'command':command,'search':search,'read':read,'web':web,'scan':scan,'computer':computer,'knowledge':knowledge,'memory':memory,'generate':generate}
names=sys.argv[2:] or list(SPEC)
for name in names:
    frames,_=SPEC[name]; rows=math.ceil(frames/10)
    sheet=Image.new('RGBA',(10*CELL,rows*CELL),(255,255,255,0))
    for f in range(frames):
        c=C(); FN[name](c,f/frames)
        a=c.im.resize((CELL,CELL),Image.LANCZOS)
        cell=Image.new('RGBA',(CELL,CELL),(255,255,255,0)); cell.putalpha(a)
        sheet.paste(cell,((f%10)*CELL,(f//10)*CELL))
    sheet.save(f'{OUT}/tool_motion_{name}.webp','WEBP',lossless=True,quality=100,method=6)
    print(name,frames,sheet.size,os.path.getsize(f'{OUT}/tool_motion_{name}.webp'))
