#!/usr/bin/env python3
"""Choose NABU's languages: the fewest languages whose countries cover the
most land. Greedy set cover over countries (land area, official or
co-official languages). A country counts as covered when any of its
official languages is chosen. Antarctica (no population) is excluded.

Areas in thousands of km² (UN/CIA World Factbook, rounded).
Run:  python3 docs/language_coverage.py
"""
C = [  # (country, area, official/co-official Wikipedia language codes)
("Russia",17098,"ru"),("Canada",9985,"en fr"),("United States",9834,"en"),("China",9597,"zh"),
("Brazil",8516,"pt"),("Australia",7692,"en"),("India",3287,"hi en"),("Argentina",2780,"es"),
("Kazakhstan",2725,"kk ru"),("Algeria",2382,"ar"),("DR Congo",2345,"fr"),("Greenland",2166,"kl da"),
("Saudi Arabia",2150,"ar"),("Mexico",1964,"es"),("Indonesia",1905,"id"),("Sudan",1886,"ar en"),
("Libya",1760,"ar"),("Iran",1648,"fa"),("Mongolia",1564,"mn"),("Peru",1285,"es"),("Chad",1284,"fr ar"),
("Niger",1267,"fr"),("Angola",1247,"pt"),("Mali",1240,"fr"),("South Africa",1221,"en af zu xh"),
("Colombia",1142,"es"),("Ethiopia",1104,"am"),("Bolivia",1099,"es"),("Mauritania",1031,"ar"),
("Egypt",1002,"ar"),("Tanzania",947,"sw en"),("Nigeria",924,"en"),("Venezuela",916,"es"),
("Pakistan",881,"ur en"),("Namibia",825,"en"),("Mozambique",801,"pt"),("Turkey",784,"tr"),
("Chile",756,"es"),("Zambia",753,"en"),("Myanmar",677,"my"),("Afghanistan",652,"ps fa"),
("South Sudan",644,"en"),("France",644,"fr"),("Somalia",638,"so ar"),("Central African Rep.",623,"fr"),
("Ukraine",604,"uk"),("Madagascar",587,"mg fr"),("Botswana",582,"en"),("Kenya",580,"sw en"),
("Yemen",528,"ar"),("Thailand",513,"th"),("Spain",506,"es"),("Turkmenistan",488,"tk"),
("Cameroon",475,"fr en"),("Papua New Guinea",463,"en"),("Sweden",450,"sv"),("Uzbekistan",449,"uz"),
("Morocco",447,"ar"),("Iraq",438,"ar"),("Paraguay",407,"es"),("Zimbabwe",391,"en"),("Norway",385,"no"),
("Japan",378,"ja"),("Germany",357,"de"),("Congo",342,"fr"),("Finland",338,"fi sv"),("Vietnam",331,"vi"),
("Malaysia",330,"ms"),("Côte d'Ivoire",322,"fr"),("Poland",313,"pl"),("Oman",310,"ar"),("Italy",301,"it"),
("Philippines",300,"tl en"),("Ecuador",284,"es"),("Burkina Faso",274,"fr"),("New Zealand",268,"en"),
("Gabon",268,"fr"),("Western Sahara",266,"ar"),("Guinea",246,"fr"),("United Kingdom",243,"en"),
("Uganda",242,"en sw"),("Ghana",239,"en"),("Romania",238,"ro"),("Laos",237,"lo"),("Guyana",215,"en"),
("Belarus",208,"be ru"),("Kyrgyzstan",200,"ky ru"),("Senegal",197,"fr"),("Syria",185,"ar"),
("Cambodia",181,"km"),("Uruguay",176,"es"),("Suriname",164,"nl"),("Tunisia",164,"ar"),
("Bangladesh",148,"bn"),("Nepal",147,"ne"),("Tajikistan",143,"tg ru"),("Greece",132,"el"),
("Nicaragua",130,"es"),("North Korea",121,"ko"),("Malawi",118,"en"),("Eritrea",118,"ti ar en"),
("Benin",115,"fr"),("Honduras",112,"es"),("Liberia",111,"en"),("Bulgaria",111,"bg"),("Cuba",110,"es"),
("Guatemala",109,"es"),("Iceland",103,"is"),("South Korea",100,"ko"),("Hungary",93,"hu"),
("Portugal",92,"pt"),("Jordan",89,"ar"),("Serbia",88,"sr"),("Azerbaijan",87,"az"),("Austria",84,"de"),
("UAE",84,"ar"),("Czechia",79,"cs"),("Panama",75,"es"),("Sierra Leone",72,"en"),("Ireland",70,"en ga"),
("Georgia",70,"ka"),("Sri Lanka",66,"si ta"),("Lithuania",65,"lt"),("Latvia",65,"lv"),("Togo",57,"fr"),
("Croatia",57,"hr"),("Bosnia",51,"bs hr sr"),("Costa Rica",51,"es"),("Slovakia",49,"sk"),
("Dominican Rep.",48,"es"),("Estonia",45,"et"),("Denmark",43,"da"),("Netherlands",42,"nl"),
("Switzerland",41,"de fr it"),("Bhutan",38,"dz"),("Taiwan",36,"zh"),("Guinea-Bissau",36,"pt"),
("Moldova",34,"ro"),("Belgium",31,"nl fr de"),("Lesotho",30,"en st"),("Armenia",30,"hy"),
("Solomon Islands",29,"en"),("Albania",29,"sq"),("Equatorial Guinea",28,"es fr pt"),
("Burundi",28,"fr rn"),("Haiti",28,"fr ht"),("Rwanda",26,"rw en fr sw"),("North Macedonia",26,"mk"),
("Djibouti",23,"fr ar"),("Belize",23,"en"),("Israel",22,"he"),("El Salvador",21,"es"),("Slovenia",20,"sl"),
("Fiji",18,"en"),("Kuwait",18,"ar"),("Eswatini",17,"en ss"),("Timor-Leste",15,"pt"),("Bahamas",14,"en"),
("Montenegro",14,"sr"),("Vanuatu",12,"en fr"),("Qatar",12,"ar"),("Gambia",11,"en"),("Jamaica",11,"en"),
("Kosovo",11,"sq sr"),("Lebanon",10,"ar"),("Cyprus",9,"el tr"),("Puerto Rico",9,"es en"),
("Palestine",6,"ar"),("Brunei",6,"ms"),("Trinidad and Tobago",5,"en"),("Cape Verde",4,"pt"),
]
total = sum(a for _, a, _ in C)
uncovered = {n: (a, set(l.split())) for n, a, l in C}
chosen, covered = [], 0
while uncovered:
    gain = {}
    for a, langs in uncovered.values():
        for l in langs:
            gain[l] = gain.get(l, 0) + a
    lang = max(gain, key=gain.get)
    got = [n for n, (a, ls) in uncovered.items() if lang in ls]
    covered += gain[lang]
    for n in got:
        del uncovered[n]
    chosen.append((lang, gain[lang], covered, got))
    if covered / total >= 0.90 and len(chosen) >= 1 and "--all" not in __import__("sys").argv:
        break
print(f"{'#':>2}  lang  adds (k km²)  cumulative   largest countries it adds")
for i, (l, g, c, got) in enumerate(chosen, 1):
    print(f"{i:>2}  {l:<4}  {g:>11,}  {100*c/total:>9.1f}%   {', '.join(got[:6])}{' …' if len(got) > 6 else ''}")
print(f"\nLand counted: {total:,}k km² ({len(C)} countries, ≈99% of land outside Antarctica)")
