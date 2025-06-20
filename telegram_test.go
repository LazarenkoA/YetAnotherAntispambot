package app

import (
	"Antispam/AI"
	"Antispam/AI/deepseek"
	"Antispam/AI/giga"
	mock_app "Antispam/mock"
	"context"
	"github.com/agiledragon/gomonkey/v2"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/golang/mock/gomock"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"log/slog"
	"math"
	"testing"
	"time"
)

func Test_KilledInfo(t *testing.T) {
	tmpTime, _ := time.Parse(time.DateTime, "2025-03-10 11:46:10")
	k := &KilledInfo{
		UserID: 32323,
		To:     tmpTime,
	}

	assert.Equal(t, `{"UserID":32323,"UserName":"","To":"2025-03-10T11:46:10Z"}`, k.String())
}

func Test_UserInfo(t *testing.T) {
	u := &UserInfo{
		ID:   32323,
		Name: "test",
	}

	assert.Equal(t, `{"ID":32323,"Name":"test","Weight":0}`, u.String())
}

func Test_watchKilledUsers(t *testing.T) {
	c := gomock.NewController(t)
	defer c.Finish()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mockRedis := mock_app.NewMockIRedis(c)

	telega := &Telega{ctx: ctx, cancel: cancel, r: mockRedis, logger: slog.Default()}

	tmp := (&KilledInfo{UserName: "test", To: time.Now().Add(time.Hour * -2)}).String()
	mockRedis.EXPECT().Items(killedUsers).Return([]string{tmp}, nil)
	mockRedis.EXPECT().DeleteItems(killedUsers, tmp)
	telega.watchKilledUsers(time.Second)
}

func Test_SaveMember(t *testing.T) {
	telega := &Telega{users: map[int64]map[int64]UserInfo{}, logger: slog.Default()}

	telega.SaveMember(111, &tgbotapi.User{
		ID:       21212,
		UserName: "test",
	})
	telega.SaveMember(111, &tgbotapi.User{
		ID:       323333,
		UserName: "test2",
	})

	assert.Equal(t, 1, len(telega.users))
	assert.Equal(t, 2, len(telega.users[111]))
	assert.Equal(t, 1, int(telega.users[111][323333].Weight))
	assert.Equal(t, 1, int(telega.users[111][21212].Weight))

	telega.SaveMember(111, &tgbotapi.User{
		ID:       323333,
		UserName: "test2",
	})
	telega.SaveMember(111, &tgbotapi.User{
		ID:       323333,
		UserName: "test2",
	})

	assert.Equal(t, 3, int(telega.users[111][323333].Weight))
	assert.Equal(t, 1, int(telega.users[111][21212].Weight))
}

func Test_GetRandUser(t *testing.T) {
	telega := &Telega{users: map[int64]map[int64]UserInfo{
		000: {111: UserInfo{ID: 111}},
	}, logger: slog.Default()}

	v := telega.GetRandUser(111, 0)
	assert.Nil(t, v)

	v = telega.GetRandUser(000, 0)
	if assert.NotNil(t, v) {
		assert.Equal(t, int64(111), v.ID)
	}

	telega.users[000][222] = UserInfo{ID: 222}

	assert.Equal(t, 2, len(telega.users[000]))

	v = telega.GetRandUser(000, 0)
	if assert.NotNil(t, v) {
		assert.True(t, v.ID == 111 || v.ID == 222)
	}
}

func Test_GetRandUserByWeight(t *testing.T) {
	t.Run("test1", func(t *testing.T) {
		telega := &Telega{users: map[int64]map[int64]UserInfo{
			000: {
				111: UserInfo{ID: 111, Weight: 0},
				222: UserInfo{ID: 222, Weight: 3},
				333: UserInfo{ID: 333, Weight: 4},
			},
		}, logger: slog.Default()}

		check := map[int64]float32{}
		for range 10000 {
			user := telega.GetRandUserByWeight(000, 0)
			check[user.ID]++
		}

		assert.Equal(t, float64(0), roundToTens(float64(check[111]/100000)*100))
		assert.Equal(t, float64(40), roundToTens(float64(check[222]/10000)*100))
		assert.Equal(t, float64(60), roundToTens(float64(check[333]/10000)*100))
	})
	t.Run("test2", func(t *testing.T) {
		telega := &Telega{users: map[int64]map[int64]UserInfo{
			000: {
				111: UserInfo{ID: 111, Weight: 4},
				222: UserInfo{ID: 222, Weight: 4},
				333: UserInfo{ID: 333, Weight: 4},
			},
		}, logger: slog.Default()}

		check := map[int64]float32{}
		for range 10000 {
			user := telega.GetRandUserByWeight(000, 0)
			check[user.ID]++
		}

		assert.Equal(t, float64(30), roundToTens(float64(check[111]/10000)*100))
		assert.Equal(t, float64(30), roundToTens(float64(check[222]/10000)*100))
		assert.Equal(t, float64(30), roundToTens(float64(check[333]/10000)*100))
	})
	t.Run("test3", func(t *testing.T) {
		telega := &Telega{users: map[int64]map[int64]UserInfo{
			000: {
				111: UserInfo{ID: 111, Weight: 4},
				222: UserInfo{ID: 222, Weight: 4},
				333: UserInfo{ID: 333, Weight: 44},
			},
		}, logger: slog.Default()}

		check := map[int64]float32{}
		for range 10000 {
			user := telega.GetRandUserByWeight(000, 111)
			check[user.ID]++
		}

		assert.Equal(t, float64(0), roundToTens(float64(check[111]/10000)*100))
		assert.Equal(t, float64(50), roundToTens(float64(check[222]/10000)*100))
		assert.Equal(t, float64(50), roundToTens(float64(check[333]/10000)*100))
	})
	//t.Run("test4", func(t *testing.T) {
	//	users := map[int64]UserInfo{}
	//	json.Unmarshal(testData(), &users)
	//	telega := &Telega{users: map[int64]map[int64]UserInfo{
	//		000: users,
	//	}, logger: slog.Default()}
	//
	//	tmp := map[int64]int{}
	//
	//	for range 10000 {
	//		user := telega.GetRandUserByWeight(000, 111)
	//		fmt.Println(user)
	//		tmp[user.ID]++
	//	}
	//})
}

func Test_aiClient(t *testing.T) {
	c := gomock.NewController(t)
	defer c.Finish()

	t.Run("", func(t *testing.T) {
		telega := &Telega{}
		f := telega.aiClient(123, []AIConf{{}, {}})
		result, err := f("")
		assert.Nil(t, result)
		assert.Error(t, err)
	})
	t.Run("", func(t *testing.T) {
		p := gomonkey.ApplyFunc(giga.NewGigaClient, func(ctx context.Context, authKey string) (*giga.Client, error) {
			return new(giga.Client), nil
		})
		p.ApplyFunc(deepseek.NewDSClient, func(ctx context.Context, apiKey string) (*deepseek.Client, error) {
			return new(deepseek.Client), nil
		})
		defer p.Reset()

		giga := mock_app.NewMockIMessageAnalysis(c)
		ds := mock_app.NewMockIMessageAnalysis(c)

		giga.EXPECT().GetMessageCharacteristics("test").Return(nil, errors.New("error"))
		ds.EXPECT().GetMessageCharacteristics("test").Return(&AI.MessageAnalysis{
			IsSpam: true,
		}, nil)

		telega := &Telega{logger: slog.Default()}
		telega.aiClient(123, []AIConf{{Name: "deepseek"}, {Name: "gigachat"}})
		telega.pool.Store(int64(123), []IMessageAnalysis{giga, ds})

		f := telega.aiClient(123, []AIConf{{Name: "deepseek"}, {Name: "gigachat"}})
		result, err := f("test")
		assert.NoError(t, err)
		if assert.NotNil(t, result) {
			assert.True(t, result.IsSpam)
		}
	})
}

func roundToTens(n float64) float64 {
	return math.Round(n/10) * 10
}

func testData() []byte {
	return []byte(`{"1013216770":{"ID":1013216770,"Name":"Антон ","Weight":0},"1029137578":{"ID":1029137578,"Name":"@limus80","Weight":17},"1030447811":{"ID":1030447811,"Name":"Оксана Гуляева","Weight":0},"1035178754":{"ID":1035178754,"Name":"Жания Алихан","Weight":0},"1036914483":{"ID":1036914483,"Name":"@RunForTheSun","Weight":0},"1063656291":{"ID":1063656291,"Name":"Рамзиль Хабибуллин","Weight":0},"1087656067":{"ID":1087656067,"Name":"@innuil","Weight":0},"1093672755":{"ID":1093672755,"Name":"@RustamAD","Weight":0},"110712132":{"ID":110712132,"Name":"@alkadiene","Weight":1},"111262184":{"ID":111262184,"Name":"@salangin_anton","Weight":4},"1127126690":{"ID":1127126690,"Name":"@adaevoleg","Weight":1},"1143950410":{"ID":1143950410,"Name":"@eforester","Weight":5},"1166093942":{"ID":1166093942,"Name":"Маргарита ","Weight":2},"1170201489":{"ID":1170201489,"Name":"@Far_Saidov","Weight":0},"1173588018":{"ID":1173588018,"Name":"Елена Шмакова","Weight":3},"1174959101":{"ID":1174959101,"Name":"@gwdfrl","Weight":2},"118992689":{"ID":118992689,"Name":"@tresmodiosvir","Weight":0},"1193921362":{"ID":1193921362,"Name":"Igor Lashin","Weight":0},"1224188814":{"ID":1224188814,"Name":"Владимир ","Weight":1},"1227198568":{"ID":1227198568,"Name":"@ZLbIDNEV","Weight":5},"1234913912":{"ID":1234913912,"Name":"@SuxrobDev","Weight":2},"1239553231":{"ID":1239553231,"Name":"@HuKuTaDM","Weight":0},"124727303":{"ID":124727303,"Name":"@russian_linux","Weight":1},"1255356122":{"ID":1255356122,"Name":"@AMORALlTY","Weight":0},"1285762800":{"ID":1285762800,"Name":"@toxichanton","Weight":2},"1296295010":{"ID":1296295010,"Name":"@Ritttka1","Weight":0},"1297753793":{"ID":1297753793,"Name":"@Bagand_Zainulov","Weight":2},"130979309":{"ID":130979309,"Name":"@AlexeyStoyanov","Weight":0},"1324391356":{"ID":1324391356,"Name":"@Rirhi01","Weight":0},"134554048":{"ID":134554048,"Name":"@DaniilKhokhlachev","Weight":0},"1345879650":{"ID":1345879650,"Name":"@IgorT_1C","Weight":0},"136699748":{"ID":136699748,"Name":"@Buba_Castorski","Weight":0},"1371357124":{"ID":1371357124,"Name":"Vasya Golovach","Weight":0},"1374764875":{"ID":1374764875,"Name":"@cycle90m","Weight":0},"137912831":{"ID":137912831,"Name":"@alexandrzinov","Weight":0},"138978214":{"ID":138978214,"Name":"@SashaErem","Weight":0},"1394943200":{"ID":1394943200,"Name":"@irinmalin","Weight":0},"1406022058":{"ID":1406022058,"Name":"Nataphik A","Weight":3},"1410021056":{"ID":1410021056,"Name":"@shersh_rarus","Weight":0},"1411146509":{"ID":1411146509,"Name":"@VTrofimova2003","Weight":0},"142693398":{"ID":142693398,"Name":"@ReWelder","Weight":0},"145234341":{"ID":145234341,"Name":"@arkarimov","Weight":3},"1452694073":{"ID":1452694073,"Name":"@Anatoliy_khrysev","Weight":1},"145589767":{"ID":145589767,"Name":"@mongodel","Weight":1},"1461170782":{"ID":1461170782,"Name":"@dimingos","Weight":12},"1469430215":{"ID":1469430215,"Name":"@VaPakh","Weight":86},"1472966478":{"ID":1472966478,"Name":"Наталья ","Weight":0},"1486565287":{"ID":1486565287,"Name":"@DMRLMV","Weight":2},"152299746":{"ID":152299746,"Name":"Алексей ","Weight":1},"1573659679":{"ID":1573659679,"Name":"@Roman_T_nsk","Weight":0},"1584184552":{"ID":1584184552,"Name":"Ildar ","Weight":0},"1612148804":{"ID":1612148804,"Name":"@aenye_01011110","Weight":0},"162673255":{"ID":162673255,"Name":"@Markov_Denis_V","Weight":0},"164680905":{"ID":164680905,"Name":"@KotovDima","Weight":0},"1653827432":{"ID":1653827432,"Name":"@OMGARROW","Weight":0},"166331781":{"ID":166331781,"Name":"@KovAlexey","Weight":8},"1664652205":{"ID":1664652205,"Name":"@ROk_dev","Weight":0},"1678837114":{"ID":1678837114,"Name":"@KovalevYura","Weight":0},"172070948":{"ID":172070948,"Name":"@potap77","Weight":0},"1747949266":{"ID":1747949266,"Name":"@notcipaa","Weight":1},"1758321032":{"ID":1758321032,"Name":"@InvisibleBat","Weight":2},"1784078144":{"ID":1784078144,"Name":"@Denis1899","Weight":4},"1800373427":{"ID":1800373427,"Name":"@xtoto_tama","Weight":0},"1811734324":{"ID":1811734324,"Name":"@treggzZ","Weight":3},"1814550610":{"ID":1814550610,"Name":"@hykal143","Weight":0},"182297752":{"ID":182297752,"Name":"@iEarthMan","Weight":0},"184034228":{"ID":184034228,"Name":"@ohmyfcingod","Weight":2},"1856803883":{"ID":1856803883,"Name":"Ксения Дегтярёва","Weight":0},"1863345346":{"ID":1863345346,"Name":"@Miha_novv","Weight":1},"186806386":{"ID":186806386,"Name":"@palsergeevich","Weight":68},"1873926400":{"ID":1873926400,"Name":"@kovrovtsev","Weight":0},"191380536":{"ID":191380536,"Name":"@LeonFakeWelder","Weight":0},"191498989":{"ID":191498989,"Name":"Алексей М","Weight":0},"1923642501":{"ID":1923642501,"Name":"@starcraftenjoyer","Weight":3},"1937255124":{"ID":1937255124,"Name":"@maksjuve","Weight":0},"1942123836":{"ID":1942123836,"Name":"Светлана ","Weight":0},"1943531671":{"ID":1943531671,"Name":"Konstantin ","Weight":0},"197760519":{"ID":197760519,"Name":"@gofrom","Weight":3},"1982232553":{"ID":1982232553,"Name":"@shevd_arida","Weight":28},"2032223626":{"ID":2032223626,"Name":"Олег Балашов","Weight":2},"203845953":{"ID":203845953,"Name":"@osipovnv","Weight":0},"204670499":{"ID":204670499,"Name":"@IgorKakoitoTam","Weight":1},"2051959811":{"ID":2051959811,"Name":"@rt4412","Weight":0},"2076111743":{"ID":2076111743,"Name":"Валерия Гущина","Weight":2},"210668999":{"ID":210668999,"Name":"@anko_2000","Weight":2},"2117488824":{"ID":2117488824,"Name":"@yambsd","Weight":8},"2119221381":{"ID":2119221381,"Name":"@MobileAgentUser","Weight":0},"215097697":{"ID":215097697,"Name":"@pfilippov","Weight":0},"216244773":{"ID":216244773,"Name":"@alexaliferov","Weight":8},"223790363":{"ID":223790363,"Name":"@gzharkoj","Weight":0},"227099995":{"ID":227099995,"Name":"@NiiaZIA","Weight":2},"227860843":{"ID":227860843,"Name":"@AVBachurin","Weight":5},"229089577":{"ID":229089577,"Name":"@kam2718","Weight":0},"230092909":{"ID":230092909,"Name":"@Un_tru","Weight":0},"232010955":{"ID":232010955,"Name":"@AleksandrShumakyan","Weight":25},"232291200":{"ID":232291200,"Name":"@ValerijBel","Weight":2},"242065302":{"ID":242065302,"Name":"@rjycnfynbyvjpu","Weight":15},"242755757":{"ID":242755757,"Name":"@MikhailBelov","Weight":0},"247569203":{"ID":247569203,"Name":"@ivkuchin8","Weight":1},"248429854":{"ID":248429854,"Name":"@Geidar","Weight":3},"251159934":{"ID":251159934,"Name":"@LazarenkoAN","Weight":57},"251523311":{"ID":251523311,"Name":"@GomanOleg","Weight":12},"253922032":{"ID":253922032,"Name":"@uxidsgn","Weight":0},"258612492":{"ID":258612492,"Name":"Константин ","Weight":2},"259619203":{"ID":259619203,"Name":"@zhloby3k","Weight":1},"273148991":{"ID":273148991,"Name":"@Jimmi910","Weight":0},"278706744":{"ID":278706744,"Name":"@Mr_Softmaker","Weight":0},"280958003":{"ID":280958003,"Name":"@AN_Efremov","Weight":2},"283680079":{"ID":283680079,"Name":"@Tonik992","Weight":0},"283746115":{"ID":283746115,"Name":"Nikolay ","Weight":1},"286727894":{"ID":286727894,"Name":"@DM_XXIV","Weight":0},"287685616":{"ID":287685616,"Name":"@SheferNA","Weight":0},"296005771":{"ID":296005771,"Name":"@TheReal_1337","Weight":3},"296631612":{"ID":296631612,"Name":"@merloga","Weight":2},"297157366":{"ID":297157366,"Name":"@TimofeySin","Weight":5},"308546604":{"ID":308546604,"Name":"@SiarheiPiva","Weight":1},"313404906":{"ID":313404906,"Name":"@Malakhov_Denis","Weight":0},"313617790":{"ID":313617790,"Name":"@svsrus81","Weight":0},"314068992":{"ID":314068992,"Name":"@Eternium","Weight":0},"320656953":{"ID":320656953,"Name":"@Sidorov_Anton","Weight":0},"324925419":{"ID":324925419,"Name":"@Programmer1c7and8","Weight":4},"330239977":{"ID":330239977,"Name":"Alexey ","Weight":0},"330599944":{"ID":330599944,"Name":"@twaise","Weight":1},"334623309":{"ID":334623309,"Name":"@abalashoff","Weight":8},"338612945":{"ID":338612945,"Name":"@shcghjhg","Weight":3},"338765885":{"ID":338765885,"Name":"@isfreeman","Weight":0},"338870982":{"ID":338870982,"Name":"@rita_wei","Weight":0},"339589416":{"ID":339589416,"Name":"@fL4me","Weight":0},"344227400":{"ID":344227400,"Name":"@PavelPR96","Weight":0},"344263271":{"ID":344263271,"Name":"@joinvb","Weight":0},"346843570":{"ID":346843570,"Name":"@MaksSadykov","Weight":0},"348669527":{"ID":348669527,"Name":"@F_j_fr","Weight":2},"350506466":{"ID":350506466,"Name":"@wBazil","Weight":0},"350702398":{"ID":350702398,"Name":"Vitaliy ","Weight":0},"350850855":{"ID":350850855,"Name":"@itTungus","Weight":0},"351264536":{"ID":351264536,"Name":"@ivaniv91","Weight":11},"352520764":{"ID":352520764,"Name":"@AL0123","Weight":0},"353717942":{"ID":353717942,"Name":"@SergeyNan","Weight":1},"356584534":{"ID":356584534,"Name":"@KazimirovValentin","Weight":0},"357567490":{"ID":357567490,"Name":"@Radik_gre","Weight":0},"360714059":{"ID":360714059,"Name":"@Taras_Zhidkov","Weight":3},"371267148":{"ID":371267148,"Name":"@ryutin_eo","Weight":0},"372212059":{"ID":372212059,"Name":"@militarymax","Weight":1},"374896366":{"ID":374896366,"Name":"@hlistalin","Weight":0},"376558810":{"ID":376558810,"Name":"Дмитрий Ч ","Weight":0},"384764824":{"ID":384764824,"Name":"@capitan_nemo_spb","Weight":0},"391583900":{"ID":391583900,"Name":"Pavel Lytkin","Weight":1},"394201158":{"ID":394201158,"Name":"@VladislavZabelin","Weight":0},"399825680":{"ID":399825680,"Name":"Денис ","Weight":3},"401137007":{"ID":401137007,"Name":"@TaigaBeast","Weight":0},"402336735":{"ID":402336735,"Name":"@maxxim14","Weight":0},"402783884":{"ID":402783884,"Name":"@andreyspopov","Weight":8},"404330610":{"ID":404330610,"Name":"@taomen20","Weight":2},"406389229":{"ID":406389229,"Name":"@bapho_bush","Weight":0},"408704532":{"ID":408704532,"Name":"@eduard1_2","Weight":1},"411298528":{"ID":411298528,"Name":"@nuori_kettu","Weight":0},"416455657":{"ID":416455657,"Name":"@auto_tores_team","Weight":0},"417610404":{"ID":417610404,"Name":"@vladimir_nadulich","Weight":29},"420932475":{"ID":420932475,"Name":"@max_ryb","Weight":1},"421275715":{"ID":421275715,"Name":"@dsident","Weight":7},"421339714":{"ID":421339714,"Name":"@v_alekhnovich","Weight":2},"421926619":{"ID":421926619,"Name":"@alexxey_ag","Weight":3},"426374246":{"ID":426374246,"Name":"@Stricell","Weight":0},"430848066":{"ID":430848066,"Name":"Артур Овсянников","Weight":0},"431000692":{"ID":431000692,"Name":"@Ultradich","Weight":0},"433582644":{"ID":433582644,"Name":"@Alex2611","Weight":1},"434920992":{"ID":434920992,"Name":"@a_skovorodina","Weight":0},"436187740":{"ID":436187740,"Name":"GG ","Weight":0},"438226723":{"ID":438226723,"Name":"@eeeeeeeeeetr","Weight":0},"438490308":{"ID":438490308,"Name":"@nikitaburyakov","Weight":22},"439731160":{"ID":439731160,"Name":"@alshadTG","Weight":0},"440214838":{"ID":440214838,"Name":"@mummytroll1974","Weight":0},"442291656":{"ID":442291656,"Name":"@jONES1979","Weight":0},"445084018":{"ID":445084018,"Name":"@DonGouzen","Weight":1},"446884887":{"ID":446884887,"Name":"@denis8345","Weight":1},"448419035":{"ID":448419035,"Name":"@proDOOMman","Weight":0},"449423482":{"ID":449423482,"Name":"@DrKoteika","Weight":17},"450382913":{"ID":450382913,"Name":"@sibkron","Weight":0},"461095136":{"ID":461095136,"Name":"@evgeny_serin","Weight":0},"462703804":{"ID":462703804,"Name":"@DTishenko","Weight":19},"474231033":{"ID":474231033,"Name":"@OdlerTelegram","Weight":0},"478894516":{"ID":478894516,"Name":"@ShandorKochish","Weight":0},"482023487":{"ID":482023487,"Name":"@m_soldatov","Weight":12},"482227208":{"ID":482227208,"Name":"@serg_7x","Weight":1},"486940569":{"ID":486940569,"Name":"@artnevs","Weight":1},"488737647":{"ID":488737647,"Name":"@Tormozit","Weight":19},"490487671":{"ID":490487671,"Name":"@VolosovEA","Weight":0},"490609314":{"ID":490609314,"Name":"@antontrunov11","Weight":0},"490999268":{"ID":490999268,"Name":"@Seg777999","Weight":0},"497712989":{"ID":497712989,"Name":"@kluevM","Weight":0},"5005053610":{"ID":5005053610,"Name":"@Oktyabrina_spb","Weight":2},"501340326":{"ID":501340326,"Name":"@undertecer","Weight":1},"5032036843":{"ID":5032036843,"Name":"Григорий ","Weight":0},"504107842":{"ID":504107842,"Name":"@Polukhin_Vladimir","Weight":1},"5047931107":{"ID":5047931107,"Name":"Артём ","Weight":1},"505275575":{"ID":505275575,"Name":"@DachCoin","Weight":0},"506898925":{"ID":506898925,"Name":"@lolkek1233214","Weight":52},"508487450":{"ID":508487450,"Name":"@CbIHok","Weight":1},"5090969266":{"ID":5090969266,"Name":"@Bchhvgkjgg","Weight":0},"512109629":{"ID":512109629,"Name":"Alexander Y. ","Weight":0},"5142365330":{"ID":5142365330,"Name":"@Ramazan_EE","Weight":1},"5150377120":{"ID":5150377120,"Name":"@SoullessExistence","Weight":0},"5152437921":{"ID":5152437921,"Name":"Дмитрий Лисунов","Weight":2},"517415295":{"ID":517415295,"Name":"@vbehtin","Weight":4},"520466377":{"ID":520466377,"Name":"@brake71","Weight":0},"5210923060":{"ID":5210923060,"Name":"@seaflame","Weight":0},"522057378":{"ID":522057378,"Name":"@i_oustinov","Weight":2},"5255168279":{"ID":5255168279,"Name":"@babushkind","Weight":0},"529422231":{"ID":529422231,"Name":"@Shammian","Weight":0},"5308906782":{"ID":5308906782,"Name":"Anjlesh Yadav","Weight":0},"5336434674":{"ID":5336434674,"Name":"@fardsx1","Weight":1},"535886182":{"ID":535886182,"Name":"@x_rikpa","Weight":0},"538306348":{"ID":538306348,"Name":"@alister_brok","Weight":1},"5390249649":{"ID":5390249649,"Name":"@StanislavMikhaylov86","Weight":16},"5399671923":{"ID":5399671923,"Name":"Dmitry S","Weight":0},"5432113846":{"ID":5432113846,"Name":"Юлия Романовская","Weight":0},"5448227988":{"ID":5448227988,"Name":"@BritishKking","Weight":8},"5458628201":{"ID":5458628201,"Name":"@Timur_eco","Weight":1},"546085625":{"ID":546085625,"Name":"@KhodakovPavel","Weight":0},"549294113":{"ID":549294113,"Name":"Евгений ","Weight":2},"557130331":{"ID":557130331,"Name":"@ViktorKV77","Weight":3},"5591323814":{"ID":5591323814,"Name":"🖤🤙🏻 ","Weight":0},"5591756718":{"ID":5591756718,"Name":"@ksenia2003spb","Weight":1},"5616967190":{"ID":5616967190,"Name":"@Sd_Petr_sidorov","Weight":0},"5625654289":{"ID":5625654289,"Name":"@AntonG_069","Weight":2},"5638569276":{"ID":5638569276,"Name":"@panda_medved","Weight":0},"5651888390":{"ID":5651888390,"Name":"Алексей ","Weight":0},"5660853370":{"ID":5660853370,"Name":"Светлана Горденко","Weight":0},"567931973":{"ID":567931973,"Name":"@Ccommandante","Weight":2},"5689566030":{"ID":5689566030,"Name":"Виктория Бородина","Weight":2},"573246953":{"ID":573246953,"Name":"@Pavlucho1919","Weight":9},"5736061620":{"ID":5736061620,"Name":"Alyona Kovaleva","Weight":0},"5747313093":{"ID":5747313093,"Name":"@zhukov_vladimir33","Weight":0},"5757301453":{"ID":5757301453,"Name":"@viola_safo","Weight":0},"576215661":{"ID":576215661,"Name":"@MixAlhimik","Weight":2},"5813524849":{"ID":5813524849,"Name":"Кристина ","Weight":0},"5864510071":{"ID":5864510071,"Name":"@Mari90123","Weight":1},"587608311":{"ID":587608311,"Name":"@KuzNikAl","Weight":2},"5899846703":{"ID":5899846703,"Name":"Валерия Дроздова","Weight":0},"591505804":{"ID":591505804,"Name":"@SbroZlo","Weight":2},"5924366872":{"ID":5924366872,"Name":"Милена Куприянова","Weight":0},"593709487":{"ID":593709487,"Name":"@mihail26may","Weight":0},"593833610":{"ID":593833610,"Name":"@gilmanow","Weight":3},"6012977593":{"ID":6012977593,"Name":"@giklilo","Weight":0},"6040817721":{"ID":6040817721,"Name":"@chensir2023","Weight":1},"6109643790":{"ID":6109643790,"Name":"@Vityahab","Weight":4},"611953644":{"ID":611953644,"Name":"@alter256","Weight":1},"613232602":{"ID":613232602,"Name":"@Perfaudit","Weight":0},"614177573":{"ID":614177573,"Name":"@Hobbit_Jedi","Weight":0},"6149362309":{"ID":6149362309,"Name":"@Anatoly78912","Weight":4},"616840614":{"ID":616840614,"Name":"@roman_kro","Weight":0},"6213711763":{"ID":6213711763,"Name":"Ксения Болдырева","Weight":0},"6217136815":{"ID":6217136815,"Name":"Ирина Большакова","Weight":0},"621856599":{"ID":621856599,"Name":"@YuMaksimova","Weight":5},"6230153941":{"ID":6230153941,"Name":"Соня Маркова","Weight":1},"624253457":{"ID":624253457,"Name":"@BorisAB","Weight":0},"6319745080":{"ID":6319745080,"Name":"Иван ","Weight":0},"6352312575":{"ID":6352312575,"Name":"Анастасия Белова","Weight":0},"6352745992":{"ID":6352745992,"Name":"К. Aleksandr ","Weight":0},"637287549":{"ID":637287549,"Name":"@v8usr","Weight":4},"6381925030":{"ID":6381925030,"Name":"@potapkina_olga","Weight":0},"64151924":{"ID":64151924,"Name":"@Basil","Weight":2},"6446118480":{"ID":6446118480,"Name":"Júlia ","Weight":0},"646766444":{"ID":646766444,"Name":"@Mity1440","Weight":0},"6475433754":{"ID":6475433754,"Name":"Cheesy Chuckle Champion ","Weight":4},"6486185896":{"ID":6486185896,"Name":"Виктория Демьянова","Weight":0},"6489140937":{"ID":6489140937,"Name":"You Ok","Weight":0},"65034662":{"ID":65034662,"Name":"@Herurg","Weight":6},"652358256":{"ID":652358256,"Name":"@PavelRBK","Weight":2},"6531073":{"ID":6531073,"Name":"@bestuzheff","Weight":2},"6532791793":{"ID":6532791793,"Name":"@Dmitry47812","Weight":2},"6536285614":{"ID":6536285614,"Name":"@Alexey87310","Weight":2},"6540172824":{"ID":6540172824,"Name":"Ксения Ефимова","Weight":0},"6550577558":{"ID":6550577558,"Name":"@dpomog","Weight":0},"6559140340":{"ID":6559140340,"Name":"Сергей ","Weight":0},"6585280306":{"ID":6585280306,"Name":"Алексей ","Weight":0},"6603845230":{"ID":6603845230,"Name":"الثقه موجود بس الحذر واجب..... ","Weight":1},"6616064602":{"ID":6616064602,"Name":"@WPWlKG","Weight":0},"663734507":{"ID":663734507,"Name":"Александра Бессонова","Weight":2},"6638116487":{"ID":6638116487,"Name":"@Yjoiyr","Weight":0},"6682299554":{"ID":6682299554,"Name":"منيف. القحطاني","Weight":0},"6697730777":{"ID":6697730777,"Name":"Светлана Исакова","Weight":0},"6702603610":{"ID":6702603610,"Name":"@debik133","Weight":1},"6708762026":{"ID":6708762026,"Name":"Елизавета Фролова","Weight":0},"6718446340":{"ID":6718446340,"Name":"Lisa ","Weight":0},"671962273":{"ID":671962273,"Name":"@rentonekb","Weight":2},"6730646844":{"ID":6730646844,"Name":"Оксана Жукова","Weight":0},"6731947220":{"ID":6731947220,"Name":"@oleg_anisimov","Weight":0},"6734621966":{"ID":6734621966,"Name":"Ирина Николаева","Weight":6},"6744040249":{"ID":6744040249,"Name":"@AArtur95","Weight":1},"6744549268":{"ID":6744549268,"Name":"Мирослава Холодова","Weight":0},"6759268744":{"ID":6759268744,"Name":"Надежда Антонова","Weight":1},"676998014":{"ID":676998014,"Name":"@sdf1979","Weight":41},"6782591729":{"ID":6782591729,"Name":"@denny_samoilov","Weight":0},"6785077644":{"ID":6785077644,"Name":"@PStepanov93","Weight":0},"6798663004":{"ID":6798663004,"Name":"@Belkova5","Weight":1},"680753877":{"ID":680753877,"Name":"@japaleno","Weight":0},"6808653271":{"ID":6808653271,"Name":"@vlad_kondry98","Weight":1},"682060469":{"ID":682060469,"Name":"@Losyash1C","Weight":13},"683392189":{"ID":683392189,"Name":"@DKuchma","Weight":0},"6848152696":{"ID":6848152696,"Name":". .","Weight":0},"6855914062":{"ID":6855914062,"Name":"@dltUZA","Weight":0},"688409331":{"ID":688409331,"Name":"@denis_pike","Weight":0},"6902078964":{"ID":6902078964,"Name":"Анастасия Дементьева","Weight":0},"69169190":{"ID":69169190,"Name":"Rvv ","Weight":2},"691844264":{"ID":691844264,"Name":"@TonyRyz","Weight":0},"6969931032":{"ID":6969931032,"Name":"Александра Кузнецова","Weight":1},"6991819311":{"ID":6991819311,"Name":"@evellynelove","Weight":0},"703370280":{"ID":703370280,"Name":"@pershikoff","Weight":1},"7033761894":{"ID":7033761894,"Name":"Екатерина Захарова","Weight":0},"7088243115":{"ID":7088243115,"Name":"Маша ","Weight":2},"708899629":{"ID":708899629,"Name":"@UniqueNameInTelegram","Weight":3},"7142374545":{"ID":7142374545,"Name":"@Carroll_Joyner78","Weight":0},"715234338":{"ID":715234338,"Name":"@tohych","Weight":20},"715728421":{"ID":715728421,"Name":"@vt1nlfu","Weight":2},"7210415212":{"ID":7210415212,"Name":"Дарина Михайлова","Weight":1},"7213039644":{"ID":7213039644,"Name":"Jhonistain ","Weight":1},"7220312262":{"ID":7220312262,"Name":"@ChamadeShambled","Weight":0},"7222667799":{"ID":7222667799,"Name":"@Stacey_Johnson74z","Weight":1},"7238186161":{"ID":7238186161,"Name":"София ","Weight":1},"7238430283":{"ID":7238430283,"Name":"пася ","Weight":0},"7249620457":{"ID":7249620457,"Name":"Людмила Романова","Weight":1},"7255182730":{"ID":7255182730,"Name":"@Stanford_JohnsonU98","Weight":0},"7297720410":{"ID":7297720410,"Name":"Мирослава Знаменская","Weight":0},"7302450509":{"ID":7302450509,"Name":"@gassagah","Weight":2},"7372555828":{"ID":7372555828,"Name":"Егор Андреев","Weight":0},"7380021010":{"ID":7380021010,"Name":"@ArcturiaCanto","Weight":0},"738311336":{"ID":738311336,"Name":"@United_Mark","Weight":0},"7383380005":{"ID":7383380005,"Name":"@Arcanal_chaudhry","Weight":0},"741640387":{"ID":741640387,"Name":"@Da_kopman","Weight":4},"751013923":{"ID":751013923,"Name":"Alexandr Shemyakin","Weight":0},"751047161":{"ID":751047161,"Name":"@yury_ui1","Weight":0},"7542009565":{"ID":7542009565,"Name":"@Bogland_deformative","Weight":0},"7544158089":{"ID":7544158089,"Name":"Вадим ","Weight":0},"7560261477":{"ID":7560261477,"Name":"Solomon Ashley","Weight":0},"7560656905":{"ID":7560656905,"Name":"Wendie Dincher","Weight":0},"7572793729":{"ID":7572793729,"Name":"وهدان اللورد ال وهدان ","Weight":1},"759370402":{"ID":759370402,"Name":"@akatnikov","Weight":0},"7608671741":{"ID":7608671741,"Name":"Michelle Morris","Weight":1},"7632434318":{"ID":7632434318,"Name":"Любовь Другакова","Weight":1},"7633497400":{"ID":7633497400,"Name":"карим ","Weight":3},"7636169078":{"ID":7636169078,"Name":"@Carol_Sampson79","Weight":0},"7637250462":{"ID":7637250462,"Name":"@ViktorAndreew","Weight":0},"7643291352":{"ID":7643291352,"Name":"Polina ","Weight":2},"7654133027":{"ID":7654133027,"Name":"Spencer Armstrong","Weight":0},"7672884585":{"ID":7672884585,"Name":"@Carter_Bernardd","Weight":0},"767623780":{"ID":767623780,"Name":"@manul1C","Weight":21},"7697712022":{"ID":7697712022,"Name":"Coach ","Weight":0},"7702421383":{"ID":7702421383,"Name":"Elileonenetha Cldtos","Weight":1},"7708146554":{"ID":7708146554,"Name":"Dorynta Rorrishargarte","Weight":1},"7714100434":{"ID":7714100434,"Name":"Серафима ","Weight":0},"7729795886":{"ID":7729795886,"Name":"Виталий Переверзев","Weight":1},"7735075053":{"ID":7735075053,"Name":"Omprakash ","Weight":0},"7746194323":{"ID":7746194323,"Name":"Алина Каримова","Weight":1},"775068974":{"ID":775068974,"Name":"@Yellow_SubmarineIAm","Weight":2},"7754386854":{"ID":7754386854,"Name":"@Alex89ki","Weight":0},"7757219410":{"ID":7757219410,"Name":"@farryshunter","Weight":0},"7767335288":{"ID":7767335288,"Name":"Уэйк Криетев","Weight":0},"7772777714":{"ID":7772777714,"Name":"@Knudsuig_vli","Weight":1},"778082498":{"ID":778082498,"Name":"@ssroracle","Weight":5},"7788967883":{"ID":7788967883,"Name":"@AxiomaticBaht","Weight":0},"7800114449":{"ID":7800114449,"Name":"@taSFqX","Weight":0},"7800781652":{"ID":7800781652,"Name":"@PeachCatFan","Weight":0},"7800948091":{"ID":7800948091,"Name":"@Sid_AlvarezJ24","Weight":0},"7825237933":{"ID":7825237933,"Name":"@vemek19467","Weight":0},"7825812721":{"ID":7825812721,"Name":"اشرف عجور ","Weight":0},"783272415":{"ID":783272415,"Name":"Sergey R","Weight":0},"784536090":{"ID":784536090,"Name":"@error_way","Weight":1},"784558847":{"ID":784558847,"Name":"@Zvgenia","Weight":1},"7858419090":{"ID":7858419090,"Name":"احمد الجميل ","Weight":0},"786457505":{"ID":786457505,"Name":"@Nikolay_Po","Weight":0},"7885754613":{"ID":7885754613,"Name":"Preston Fraley ","Weight":0},"790524842":{"ID":790524842,"Name":"@roman_mrb16","Weight":2},"7914670354":{"ID":7914670354,"Name":"@arinnreiii","Weight":1},"7930703698":{"ID":7930703698,"Name":"Night ","Weight":1},"7944322562":{"ID":7944322562,"Name":"Мирослава Королёва","Weight":0},"7947570164":{"ID":7947570164,"Name":"@stvn7l","Weight":1},"7965802540":{"ID":7965802540,"Name":"Всеволод Никифоров","Weight":2},"7966813934":{"ID":7966813934,"Name":"Миша ","Weight":1},"7984581666":{"ID":7984581666,"Name":"Светлан Калаев","Weight":0},"802532767":{"ID":802532767,"Name":"@Irbis_Planet","Weight":2},"803978259":{"ID":803978259,"Name":"@belov_vitaly","Weight":0},"8058801259":{"ID":8058801259,"Name":"Stacy Jordan","Weight":0},"8098174434":{"ID":8098174434,"Name":"Марк Орлов","Weight":0},"8121110292":{"ID":8121110292,"Name":"@tesss11119","Weight":0},"8132007808":{"ID":8132007808,"Name":"Natally ","Weight":0},"8142823969":{"ID":8142823969,"Name":"@r_faridovich","Weight":0},"8151638916":{"ID":8151638916,"Name":"Александр Williams","Weight":2},"8180232469":{"ID":8180232469,"Name":"@ViktorDoki","Weight":0},"8192454875":{"ID":8192454875,"Name":"محمود الزملوط ","Weight":1},"8196809342":{"ID":8196809342,"Name":"عرفه كارم ","Weight":0},"839760441":{"ID":839760441,"Name":"@s0list","Weight":0},"842389906":{"ID":842389906,"Name":"@SAShikutkin","Weight":1},"867629719":{"ID":867629719,"Name":"@AncientEmperor","Weight":1},"869302380":{"ID":869302380,"Name":"@NikolayVerhovcev","Weight":2},"870910497":{"ID":870910497,"Name":"@PilgrimMaksim","Weight":0},"878185821":{"ID":878185821,"Name":"@Hzjtxgkcgkx","Weight":4},"889088156":{"ID":889088156,"Name":"Андрей Ко","Weight":0},"891274544":{"ID":891274544,"Name":"@MakarovSergey22","Weight":7},"895246572":{"ID":895246572,"Name":"@cptnflint","Weight":0},"89749612":{"ID":89749612,"Name":"@EGershon","Weight":0},"903731576":{"ID":903731576,"Name":"@SapalevEV","Weight":0},"905253232":{"ID":905253232,"Name":"@MaximkaPl","Weight":0},"910947298":{"ID":910947298,"Name":"@MishGun1C","Weight":0},"918435654":{"ID":918435654,"Name":"@nova_lake","Weight":0},"92295305":{"ID":92295305,"Name":"Владимир Алдошин","Weight":1},"938421192":{"ID":938421192,"Name":"@froloid","Weight":1},"949535777":{"ID":949535777,"Name":"Yu ","Weight":2},"950182793":{"ID":950182793,"Name":"@gulffHA","Weight":2},"95072968":{"ID":95072968,"Name":"@volkanin","Weight":4},"951246524":{"ID":951246524,"Name":"@AGMitrofanov","Weight":1},"959675857":{"ID":959675857,"Name":"@JohnPoebot","Weight":35},"959759943":{"ID":959759943,"Name":"@ErmolaevVadim","Weight":0},"961294512":{"ID":961294512,"Name":"@tyurin_ip","Weight":0},"962230916":{"ID":962230916,"Name":"@MU_Tinin","Weight":0},"962584194":{"ID":962584194,"Name":"@ogonek_sergei","Weight":1},"96297036":{"ID":96297036,"Name":"@Samosval","Weight":1},"973032673":{"ID":973032673,"Name":"@akpaevj","Weight":7},"981033842":{"ID":981033842,"Name":"@Lykov_Oleg","Weight":6},"988732646":{"ID":988732646,"Name":"Дмитрий ","Weight":0},"99219275":{"ID":99219275,"Name":"@vp_exe","Weight":0}}`)
}
