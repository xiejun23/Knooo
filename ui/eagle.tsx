import eagle from './assets/eagle.png?no-inline';

export function EagleMark({flying=false}: {flying?:boolean}) {
 return <img className={flying ? 'eagle-inline' : 'eagle-mark'} src={eagle} alt=""/>;
}

export function EagleLoading({label}: {label:string}) {
 return <div className="eagle-loading" role="status">
  <img className="eagle-flight" src={eagle} alt="" width={72} height={72}/>
  <strong>{label}</strong>
 </div>;
}
