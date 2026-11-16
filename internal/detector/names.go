package detector

import (
	"regexp"
	"strings"
)

// Common Russian first names and everyday short forms, nominative. Names whose
// forms are everyday words (Люда→люди, Поля→поле, Валя→вали, Варя→варю,
// Люба→любой) are left out. A name in a
// message is enough for the counter prefilter: whether the context is romantic
// is the model's call («с ваней набила бы хоть на лбу» has no love words).
const commonNames = `
Александр Саша Саня Шура Алексей Леша Алеша Андрей Антон Артем Артур Богдан Борис Вадим Валера Валентин
Василий Вася Виктор Витя Виталий Влад Владислав Владимир Вова Володя Всеволод Сева Вячеслав Слава
Глеб Гоша Григорий Гриша Давид Даниил Даня Денис Дима Дмитрий Евгений Женя Егор Захар Иван Ваня
Игорь Илья Кирилл Киря Костя Константин Лев Лева Леонид Леня Макар Максим Макс Марк Матвей Миша
Михаил Никита Николай Коля Олег Павел Паша Петр Петя Роман Рома Руслан Сергей Сережа Серега Степан
Степа Тимофей Тимур Тима Федор Федя Филипп Эдуард Эдик Юра Юрий Ярослав Яша Гена Толя Стас Семен
Анна Аня Анастасия Настя Алена Алина Алиса Алла Ангелина Анжела Валентина   Валерия Лера Варвара
  Вера Вероника Ника Виктория Вика Галина Галя Дарья Даша Диана Ева Евгения Екатерина Катя Елена
Лена Елизавета Лиза Жанна Зоя Злата Инна Ирина Ира Карина Кира Кристина Ксения Ксюша Лариса Лиля
    Людмила   Маргарита Рита Марина Мария Маша Милана Мила Надежда Надя Наталья Наташа
Нина Оксана Ольга Оля Полина   Регина Света Светлана Соня София Софья Таисия Тамара Таня Татьяна
Ульяна Юлия Юля Яна Ярослава Эля Эльвира Камилла Арина Ксюха Маруся Сонечка Танюша Катюша Машенька
`

// nameRe matches any of commonNames in any case form (Ваня, Ване, Ваней, Ваню).
var nameRe = func() *regexp.Regexp {
	var alts []string
	for _, n := range strings.Fields(commonNames) {
		alts = append(alts, nameForms(strings.ToLower(n)))
	}
	return regexp.MustCompile(`(?i)(?:^|[^\p{L}])(?:` + strings.Join(alts, "|") + `)(?:$|[^\p{L}])`)
}()

// nameForms returns a regexp for the declension of a nominative name.
func nameForms(n string) string {
	r := []rune(n)
	last := string(r[len(r)-1])
	stem := string(r[:len(r)-1])
	switch last {
	case "а", "я": // Ваня, Маша, Мария: -а/-я, -ы/-и, -е, -у/-ю, -ой/-ей, -ою/-ею
		return stem + `(?:а|я|ы|и|е|у|ю|ой|ей|ою|ею)`
	case "й": // Андрей, Сергей, Алексей: -й, -я, -ю, -ем, -е
		return stem + `(?:й|я|ю|ем|е)`
	case "ь": // Игорь, Любовь
		return stem + `(?:ь|я|ю|ем|е|и|ью)`
	default: // Иван, Олег: -, -а, -у, -ом, -е
		return n + `(?:|а|у|ом|е)`
	}
}

// MentionsName reports whether text contains a common Russian first name.
func MentionsName(text string) bool {
	return nameRe.MatchString(strings.ReplaceAll(strings.ReplaceAll(text, "ё", "е"), "Ё", "Е"))
}
